package db

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Spend reporting: per-key / per-model / per-day rollups of the charge ledger,
// backing both the personal usage dashboard and the enterprise team view.
//
// Two invariants hold across every query in this file.
//
// Scope isolation. Each query is pinned to exactly one billing subject — a
// workspace (team view) or a user (personal view) — and the predicate is emitted
// by one shared helper, so a caller cannot forget it and accidentally aggregate
// across tenants. A workspace scope never widens to another space; a member's
// PERSONAL keys bill their personal workspace and so cannot appear in a team
// report at all.
//
// No upstream identity. wallet_tx carries no auth_id, credential label or
// provider — there is simply nothing here a space admin could use to see the
// upstream fleet. (The cc-core requestlog DOES carry AuthID/AuthLabel, which is
// one of the reasons these reports are built on the ledger instead.)
//
// A note on counting: a charge row is a BILLABLE EVENT, not a request. One
// /v1/messages call writes an extra row per advisor sub-model, and writes none at
// all when the cost rounds to zero or the overdraft floor clamps it. Callers must
// surface this as "charge events", never as "requests".

// Scope pins a report to one billing subject. Exactly one field is set.
type Scope struct {
	WorkspaceID int64 // team view
	UserID      int64 // personal view
}

// WorkspaceScope reports on everything billed to one workspace, across members.
func WorkspaceScope(id int64) Scope { return Scope{WorkspaceID: id} }

// UserScope reports on everything one user spent, across the workspaces they bill.
func UserScope(id int64) Scope { return Scope{UserID: id} }

// ReportFilter is the shared query shape. Zero-valued fields mean "no filter",
// except Scope, which is mandatory.
type ReportFilter struct {
	Scope
	From, To      time.Time // half-open [From, To); zero = unbounded
	TokenID       int64     // 0 = all keys
	Model         string    // "" = all models
	Tag           string    // "" = all; matched against user_tokens.tags
	Limit, Offset int
}

// jsonTagsExpr guards json_each against the tags column's empty-string default,
// which is NOT valid JSON: json_each on it raises "malformed JSON" and takes the
// whole query down the moment a single untagged key exists in the workspace.
// json_each('[]') yields zero rows, so untagged keys correctly contribute nothing.
const jsonTagsExpr = `CASE WHEN json_valid(t.tags) THEN t.tags ELSE '[]' END`

// where builds the shared predicate. The scope clause is always emitted — that is
// the whole point of routing every report through here.
func (f ReportFilter) where() (string, []any) {
	clauses := []string{`w.kind = 'charge'`}
	args := []any{}

	switch {
	case f.WorkspaceID > 0:
		clauses = append(clauses, `w.workspace_id = ?`)
		args = append(args, f.WorkspaceID)
	case f.UserID > 0:
		clauses = append(clauses, `w.user_id = ?`)
		args = append(args, f.UserID)
	default:
		// Fail closed: an unscoped report would leak across tenants. Callers are
		// all internal, so this is a programming error, not a user error.
		clauses = append(clauses, `1 = 0`)
	}

	if !f.From.IsZero() {
		clauses = append(clauses, `w.created_at >= ?`)
		args = append(args, f.From.Unix())
	}
	if !f.To.IsZero() {
		clauses = append(clauses, `w.created_at < ?`)
		args = append(args, f.To.Unix())
	}
	if f.TokenID > 0 {
		clauses = append(clauses, `w.token_id = ?`)
		args = append(args, f.TokenID)
	}
	if f.Model != "" {
		clauses = append(clauses, `w.model = ?`)
		args = append(args, f.Model)
	}
	if f.Tag != "" {
		clauses = append(clauses,
			`EXISTS (SELECT 1 FROM json_each(`+jsonTagsExpr+`) je WHERE je.value = ?)`)
		args = append(args, f.Tag)
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

// breakdownIndexHint pins SpendBreakdown to the v16 covering index for the
// filter's scope. Empty when the filter has no scope (the fail-closed branch),
// which never returns rows anyway.
func (f ReportFilter) breakdownIndexHint() string {
	switch {
	case f.WorkspaceID > 0:
		return ` INDEXED BY idx_wallet_tx_ws_charge`
	case f.UserID > 0:
		return ` INDEXED BY idx_wallet_tx_user_charge`
	default:
		return ""
	}
}

// TokenSpend is one key's rollup over the filtered window.
type TokenSpend struct {
	TokenID int64
	Name    string
	Tags    []string
	UserID  int64  // the key's owner; meaningful in a workspace scope
	Email   string // owner's email; the "which employee" axis for the team view
	Deleted bool   // the key is gone but its ledger rows remain

	SpentUSD     float64
	ChargeEvents int64 // billable events, NOT requests

	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheCreateTokens int64

	FirstAt time.Time
	LastAt  time.Time
}

// tokenSpendSelect is shared by SpendByToken and its total. The LEFT JOIN is
// load-bearing: DeleteUserToken leaves orphaned ledger rows behind, and an inner
// join would silently drop their spend — making the per-key breakdown fail to
// reconcile with the workspace total, which is exactly the kind of discrepancy a
// finance team will (rightly) not trust.
//
// G101 fires on the word "token" in a long string literal; this is a SELECT over
// the wallet_tx/user_tokens join, and it never selects user_tokens.token.
//
//nolint:gosec // G101 false positive — see above.
const tokenSpendSelect = `
SELECT w.token_id,
       COALESCE(t.name, ''), COALESCE(t.tags, ''),
       COALESCE(t.user_id, 0), COALESCE(u.email, ''),
       (t.id IS NULL) AS deleted,
       COALESCE(SUM(-w.amount_usd), 0), COUNT(*),
       COALESCE(SUM(w.input_tokens), 0), COALESCE(SUM(w.output_tokens), 0),
       COALESCE(SUM(w.cache_read_tokens), 0), COALESCE(SUM(w.cache_create_tokens), 0),
       COALESCE(MIN(w.created_at), 0), COALESCE(MAX(w.created_at), 0)
  FROM wallet_tx w
  LEFT JOIN user_tokens t ON t.id = w.token_id
  LEFT JOIN users       u ON u.id = t.user_id
`

// SpendByToken rolls the ledger up per key, biggest spender first.
func (db *DB) SpendByToken(ctx context.Context, f ReportFilter) ([]*TokenSpend, error) {
	where, args := f.where()
	rows, err := db.QueryContext(ctx,
		tokenSpendSelect+where+` GROUP BY w.token_id ORDER BY SUM(-w.amount_usd) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*TokenSpend{}
	for rows.Next() {
		var s TokenSpend
		var tagsJSON string
		var deleted int
		var first, last int64
		if err := rows.Scan(&s.TokenID, &s.Name, &tagsJSON, &s.UserID, &s.Email, &deleted,
			&s.SpentUSD, &s.ChargeEvents,
			&s.InputTokens, &s.OutputTokens, &s.CacheReadTokens, &s.CacheCreateTokens,
			&first, &last); err != nil {
			return nil, err
		}
		s.Tags = parseGroupsJSON(tagsJSON)
		// token_id = 0 is the pre-v15 "unattributed" bucket, not a deleted key.
		s.Deleted = deleted != 0 && s.TokenID != 0
		if first > 0 {
			s.FirstAt = time.Unix(first, 0)
		}
		if last > 0 {
			s.LastAt = time.Unix(last, 0)
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// TagSpend is one label's rollup.
//
// A key carrying N tags is counted once under EACH of them, so summing SpentUSD
// across tags can exceed the workspace total. That is the standard label-analytics
// semantic (a key really did belong to both "研发部" and "前端"), but the UI must
// present it as a breakdown BY LABEL, never as a partition of the total.
type TagSpend struct {
	Tag          string  `json:"tag"`
	SpentUSD     float64 `json:"spent_usd"`
	ChargeEvents int64   `json:"charge_events"`
	Tokens       int64   `json:"tokens"`
}

// SpendByTag rolls the ledger up per label.
func (db *DB) SpendByTag(ctx context.Context, f ReportFilter) ([]*TagSpend, error) {
	where, args := f.where()
	rows, err := db.QueryContext(ctx, `
		SELECT je.value,
		       COALESCE(SUM(-w.amount_usd), 0), COUNT(*), COUNT(DISTINCT w.token_id)
		  FROM wallet_tx w
		  JOIN user_tokens t ON t.id = w.token_id
		  JOIN json_each(`+jsonTagsExpr+`) je
		`+where+` GROUP BY je.value ORDER BY 2 DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*TagSpend{}
	for rows.Next() {
		var s TagSpend
		if err := rows.Scan(&s.Tag, &s.SpentUSD, &s.ChargeEvents, &s.Tokens); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// ModelSpend is one model's rollup.
type ModelSpend struct {
	Model             string  `json:"model"`
	SpentUSD          float64 `json:"spent_usd"`
	ChargeEvents      int64   `json:"charge_events"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CacheReadTokens   int64   `json:"cache_read_tokens"`
	CacheCreateTokens int64   `json:"cache_create_tokens"`
}

// SpendByModel rolls the ledger up per model. Pre-v15 rows whose model couldn't
// be recovered from the legacy ref land under "" and are surfaced as such rather
// than being dropped.
func (db *DB) SpendByModel(ctx context.Context, f ReportFilter) ([]*ModelSpend, error) {
	where, args := f.where()
	join := ""
	if f.Tag != "" {
		join = ` JOIN user_tokens t ON t.id = w.token_id`
	}
	rows, err := db.QueryContext(ctx, `
		SELECT w.model,
		       COALESCE(SUM(-w.amount_usd), 0), COUNT(*),
		       COALESCE(SUM(w.input_tokens), 0), COALESCE(SUM(w.output_tokens), 0),
		       COALESCE(SUM(w.cache_read_tokens), 0), COALESCE(SUM(w.cache_create_tokens), 0)
		  FROM wallet_tx w`+join+`
		`+where+` GROUP BY w.model ORDER BY 2 DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*ModelSpend{}
	for rows.Next() {
		var s ModelSpend
		if err := rows.Scan(&s.Model, &s.SpentUSD, &s.ChargeEvents,
			&s.InputTokens, &s.OutputTokens, &s.CacheReadTokens, &s.CacheCreateTokens); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// DaySpend is one calendar day's rollup (UTC), feeding the contribution heatmap.
type DaySpend struct {
	Day          string  `json:"day"` // YYYY-MM-DD
	SpentUSD     float64 `json:"spent_usd"`
	ChargeEvents int64   `json:"charge_events"`
}

// SpendByDay rolls the ledger up per UTC day, ascending, with gaps left out —
// callers zero-fill for display (see ZeroFillDays).
func (db *DB) SpendByDay(ctx context.Context, f ReportFilter) ([]*DaySpend, error) {
	where, args := f.where()
	join := ""
	if f.Tag != "" {
		join = ` JOIN user_tokens t ON t.id = w.token_id`
	}
	rows, err := db.QueryContext(ctx, `
		SELECT strftime('%Y-%m-%d', w.created_at, 'unixepoch') AS day,
		       COALESCE(SUM(-w.amount_usd), 0), COUNT(*)
		  FROM wallet_tx w`+join+`
		`+where+` GROUP BY day ORDER BY day`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*DaySpend{}
	for rows.Next() {
		var s DaySpend
		if err := rows.Scan(&s.Day, &s.SpentUSD, &s.ChargeEvents); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// ZeroFillDays densifies a sparse day series over [from, to] inclusive so the
// heatmap gets one cell per calendar day.
func ZeroFillDays(sparse []*DaySpend, from, to time.Time) []*DaySpend {
	byDay := make(map[string]*DaySpend, len(sparse))
	for _, d := range sparse {
		byDay[d.Day] = d
	}
	out := []*DaySpend{}
	for d := from.UTC().Truncate(24 * time.Hour); !d.After(to.UTC()); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		if hit, ok := byDay[key]; ok {
			out = append(out, hit)
			continue
		}
		out = append(out, &DaySpend{Day: key})
	}
	return out
}

// ComputeStreaks walks a set of active days and returns the current and longest
// runs of consecutive daily activity.
//
// Computed in Go rather than SQL: the day series is already being fetched for the
// heatmap, so this costs no extra I/O, and a pure function is far easier to pin
// down than the gaps-and-islands SQL formulation.
//
// A run counts as "current" only if it reaches today or yesterday — yesterday
// because a user mid-streak simply hasn't used the service yet today, and cutting
// their streak at midnight UTC would be punishing. A run that ended three days ago
// is history, not a streak.
func ComputeStreaks(days []*DaySpend, today time.Time) (current, longest int) {
	active := make(map[string]bool, len(days))
	for _, d := range days {
		if d.ChargeEvents > 0 || d.SpentUSD > 0 {
			active[d.Day] = true
		}
	}
	if len(active) == 0 {
		return 0, 0
	}

	// Longest: sort the active days and scan for consecutive runs. ISO dates sort
	// lexicographically, so a plain string sort is chronological.
	sorted := make([]string, 0, len(active))
	for day := range active {
		sorted = append(sorted, day)
	}
	slices.Sort(sorted)

	run := 0
	var prev time.Time
	for _, day := range sorted {
		d, err := time.Parse("2006-01-02", day)
		if err != nil {
			continue
		}
		if run > 0 && d.Equal(prev.AddDate(0, 0, 1)) {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
		prev = d
	}

	// Current: only alive if the run includes today or yesterday.
	t := today.UTC().Truncate(24 * time.Hour)
	anchor := t
	if !active[t.Format("2006-01-02")] {
		anchor = t.AddDate(0, 0, -1)
		if !active[anchor.Format("2006-01-02")] {
			return 0, longest
		}
	}
	for d := anchor; active[d.Format("2006-01-02")]; d = d.AddDate(0, 0, -1) {
		current++
	}
	return current, longest
}

// SpendRow is one charge line, with the key and member resolved.
type SpendRow struct {
	ID        int64
	CreatedAt time.Time

	TokenID   int64
	TokenName string
	TokenTags []string

	UserID int64
	Email  string

	Model     string
	AmountUSD float64 // positive = amount spent

	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheCreateTokens int64

	// Attributed is false for pre-v15 rows whose token/model couldn't be
	// recovered from the legacy ref. The CSV renders their token counts blank
	// rather than 0, so a reader can tell "no tokens" from "not recorded".
	Attributed bool

	Ref string
}

const spendRowSelect = `
SELECT w.id, w.created_at, w.token_id, COALESCE(t.name, ''), COALESCE(t.tags, ''),
       w.user_id, COALESCE(u.email, ''), w.model, -w.amount_usd,
       w.input_tokens, w.output_tokens, w.cache_read_tokens, w.cache_create_tokens, w.ref
  FROM wallet_tx w
  LEFT JOIN user_tokens t ON t.id = w.token_id
  LEFT JOIN users       u ON u.id = w.user_id
`

func scanSpendRow(rows interface{ Scan(...any) error }) (*SpendRow, error) {
	var r SpendRow
	var created int64
	var tagsJSON string
	if err := rows.Scan(&r.ID, &created, &r.TokenID, &r.TokenName, &tagsJSON,
		&r.UserID, &r.Email, &r.Model, &r.AmountUSD,
		&r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.CacheCreateTokens, &r.Ref); err != nil {
		return nil, err
	}
	r.CreatedAt = time.Unix(created, 0)
	r.TokenTags = parseGroupsJSON(tagsJSON)
	r.Attributed = r.TokenID != 0
	return &r, nil
}

// ListSpendRows returns a page of charge lines (newest first) plus the total count.
func (db *DB) ListSpendRows(ctx context.Context, f ReportFilter) ([]*SpendRow, int, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := max(f.Offset, 0)

	where, args := f.where()
	join := ""
	if f.Tag != "" {
		join = ` JOIN user_tokens t ON t.id = w.token_id`
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wallet_tx w`+join+` `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := db.QueryContext(ctx, spendRowSelect+where+` ORDER BY w.id DESC LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []*SpendRow{}
	for rows.Next() {
		r, err := scanSpendRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// StreamSpendRows invokes fn once per matching charge line, oldest first, without
// materializing the result set — a 90-day enterprise export can run to six figures
// of rows, and buffering that into a slice would put a multi-hundred-MB, caller-
// controlled allocation in the hot path of a process that is also proxying LLM
// traffic. Stopping early is done by returning an error from fn.
func (db *DB) StreamSpendRows(ctx context.Context, f ReportFilter, fn func(*SpendRow) error) error {
	where, args := f.where()
	rows, err := db.QueryContext(ctx, spendRowSelect+where+` ORDER BY w.id ASC`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanSpendRow(rows)
		if err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// SpendTotals is the headline rollup over the filtered window.
type SpendTotals struct {
	SpentUSD          float64
	ChargeEvents      int64
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheCreateTokens int64
	ActiveTokens      int64
}

// SpendSummary computes the headline numbers in a single pass.
func (db *DB) SpendSummary(ctx context.Context, f ReportFilter) (*SpendTotals, error) {
	where, args := f.where()
	join := ""
	if f.Tag != "" {
		join = ` JOIN user_tokens t ON t.id = w.token_id`
	}
	var s SpendTotals
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(-w.amount_usd), 0), COUNT(*),
		       COALESCE(SUM(w.input_tokens), 0), COALESCE(SUM(w.output_tokens), 0),
		       COALESCE(SUM(w.cache_read_tokens), 0), COALESCE(SUM(w.cache_create_tokens), 0),
		       COUNT(DISTINCT w.token_id)
		  FROM wallet_tx w`+join+` `+where, args...).
		Scan(&s.SpentUSD, &s.ChargeEvents, &s.InputTokens, &s.OutputTokens,
			&s.CacheReadTokens, &s.CacheCreateTokens, &s.ActiveTokens)
	if err != nil {
		return nil, fmt.Errorf("spend summary: %w", err)
	}
	return &s, nil
}

// ---- Merged single-pass breakdown ----------------------------------------
//
// SpendBreakdown answers what SpendSummary + SpendByToken + SpendByModel +
// SpendByTag + SpendByDay answer together, from ONE pass over the ledger.
//
// Those five were five independent scans of the identical row set. On
// production that measured 2.3 s for a single user's 90-day window (208k charge
// rows) and there was no cache in front of it, so every dashboard load paid it
// again. The grain (token_id, model, day) is the coarsest that still supports
// all four breakdowns, and it collapses hundreds of thousands of ledger rows
// into a few hundred before Go ever sees them.
//
// The individual functions stay: /usage/tokens and the CSV export use them, and
// TestSpendBreakdownMatchesIndividualQueries asserts this agrees with them.

// SpendBreakdown is the merged rollup. Field-for-field equivalent to calling
// the five individual reports.
type SpendBreakdown struct {
	Total   *SpendTotals
	ByToken []*TokenSpend
	ByModel []*ModelSpend
	ByTag   []*TagSpend
	ByDay   []*DaySpend
}

// breakdownRow is one (token_id, model, day) cell of the merged aggregate.
type breakdownRow struct {
	tokenID           int64
	model             string
	dayEpoch          int64 // days since the Unix epoch, UTC
	spentUSD          float64
	events            int64
	inputTokens       int64
	outputTokens      int64
	cacheReadTokens   int64
	cacheCreateTokens int64
	firstAt, lastAt   int64
}

func (db *DB) SpendBreakdown(ctx context.Context, f ReportFilter) (*SpendBreakdown, error) {
	where, args := f.where()
	// The tag predicate references `t`, so the join is only emitted when it is
	// actually needed — carrying it unconditionally costs the covering index.
	join := ""
	if f.Tag != "" {
		join = ` JOIN user_tokens t ON t.id = w.token_id`
	}
	// Two deliberate departures from the individual queries, both measured on a
	// copy of production (user with 208k charge rows in the window):
	//
	//  - `created_at / 86400` instead of strftime(): identical UTC day bucket,
	//    but strftime on every row cost ~30% of the query (the driver is pure
	//    Go, so its date parsing is not cheap). The bucket is turned back into
	//    YYYY-MM-DD below.
	//  - INDEXED BY: the GROUP BY leads with token_id, so the planner picks
	//    idx_wallet_tx_{user,ws}_token to get grouping order — but the three
	//    grouping columns force a temp b-tree regardless, so that ordering buys
	//    nothing while costing the covering index and a rowid lookup per row.
	rows, err := db.QueryContext(ctx, `
		SELECT w.token_id, w.model, w.created_at / 86400 AS day,
		       COALESCE(SUM(-w.amount_usd), 0), COUNT(*),
		       COALESCE(SUM(w.input_tokens), 0), COALESCE(SUM(w.output_tokens), 0),
		       COALESCE(SUM(w.cache_read_tokens), 0), COALESCE(SUM(w.cache_create_tokens), 0),
		       COALESCE(MIN(w.created_at), 0), COALESCE(MAX(w.created_at), 0)
		  FROM wallet_tx w`+f.breakdownIndexHint()+join+`
		`+where+` GROUP BY w.token_id, w.model, day`, args...)
	if err != nil {
		return nil, fmt.Errorf("spend breakdown: %w", err)
	}
	defer rows.Close()

	cells := []breakdownRow{}
	for rows.Next() {
		var r breakdownRow
		if err := rows.Scan(&r.tokenID, &r.model, &r.dayEpoch,
			&r.spentUSD, &r.events,
			&r.inputTokens, &r.outputTokens, &r.cacheReadTokens, &r.cacheCreateTokens,
			&r.firstAt, &r.lastAt); err != nil {
			return nil, err
		}
		cells = append(cells, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := &SpendBreakdown{Total: &SpendTotals{}}
	tokenIdx := map[int64]*TokenSpend{}
	modelIdx := map[string]*ModelSpend{}
	dayIdx := map[string]*DaySpend{}

	for _, r := range cells {
		out.Total.SpentUSD += r.spentUSD
		out.Total.ChargeEvents += r.events
		out.Total.InputTokens += r.inputTokens
		out.Total.OutputTokens += r.outputTokens
		out.Total.CacheReadTokens += r.cacheReadTokens
		out.Total.CacheCreateTokens += r.cacheCreateTokens

		ts, ok := tokenIdx[r.tokenID]
		if !ok {
			ts = &TokenSpend{TokenID: r.tokenID}
			tokenIdx[r.tokenID] = ts
			out.ByToken = append(out.ByToken, ts)
		}
		ts.SpentUSD += r.spentUSD
		ts.ChargeEvents += r.events
		ts.InputTokens += r.inputTokens
		ts.OutputTokens += r.outputTokens
		ts.CacheReadTokens += r.cacheReadTokens
		ts.CacheCreateTokens += r.cacheCreateTokens
		if r.firstAt > 0 && (ts.FirstAt.IsZero() || r.firstAt < ts.FirstAt.Unix()) {
			ts.FirstAt = time.Unix(r.firstAt, 0)
		}
		if r.lastAt > 0 && (ts.LastAt.IsZero() || r.lastAt > ts.LastAt.Unix()) {
			ts.LastAt = time.Unix(r.lastAt, 0)
		}

		ms, ok := modelIdx[r.model]
		if !ok {
			ms = &ModelSpend{Model: r.model}
			modelIdx[r.model] = ms
			out.ByModel = append(out.ByModel, ms)
		}
		ms.SpentUSD += r.spentUSD
		ms.ChargeEvents += r.events
		ms.InputTokens += r.inputTokens
		ms.OutputTokens += r.outputTokens
		ms.CacheReadTokens += r.cacheReadTokens
		ms.CacheCreateTokens += r.cacheCreateTokens

		day := time.Unix(r.dayEpoch*86400, 0).UTC().Format("2006-01-02")
		ds, ok := dayIdx[day]
		if !ok {
			ds = &DaySpend{Day: day}
			dayIdx[day] = ds
			out.ByDay = append(out.ByDay, ds)
		}
		ds.SpentUSD += r.spentUSD
		ds.ChargeEvents += r.events
	}
	// SpendSummary counts DISTINCT token_id over the raw rows, so the 0
	// (pre-v15 unattributed) bucket counts as one — match that exactly.
	out.Total.ActiveTokens = int64(len(tokenIdx))

	if err := db.decorateTokenSpend(ctx, out.ByToken); err != nil {
		return nil, err
	}
	out.ByTag = tagsFromTokenSpend(out.ByToken)

	// Match the individual queries' ORDER BY. Ties are broken by the grouping
	// key so the output is deterministic — SQLite's ordering among equal sums
	// is not, and a jittering dashboard is its own kind of bug.
	sort.Slice(out.ByToken, func(i, j int) bool {
		if out.ByToken[i].SpentUSD != out.ByToken[j].SpentUSD {
			return out.ByToken[i].SpentUSD > out.ByToken[j].SpentUSD
		}
		return out.ByToken[i].TokenID < out.ByToken[j].TokenID
	})
	sort.Slice(out.ByModel, func(i, j int) bool {
		if out.ByModel[i].SpentUSD != out.ByModel[j].SpentUSD {
			return out.ByModel[i].SpentUSD > out.ByModel[j].SpentUSD
		}
		return out.ByModel[i].Model < out.ByModel[j].Model
	})
	sort.Slice(out.ByDay, func(i, j int) bool { return out.ByDay[i].Day < out.ByDay[j].Day })
	return out, nil
}

// decorateTokenSpend fills in each key's name / tags / owner. Separate from the
// aggregate because joining user_tokens into the main query costs the covering
// index — and this is a handful of rows keyed by primary key.
func (db *DB) decorateTokenSpend(ctx context.Context, spends []*TokenSpend) error {
	if len(spends) == 0 {
		return nil
	}
	ids := make([]any, 0, len(spends))
	holes := make([]string, 0, len(spends))
	for _, s := range spends {
		ids = append(ids, s.TokenID)
		holes = append(holes, "?")
	}
	//nolint:gosec // G101 false positive: user_tokens.token is never selected.
	rows, err := db.QueryContext(ctx, `
		SELECT t.id, COALESCE(t.name, ''), COALESCE(t.tags, ''),
		       COALESCE(t.user_id, 0), COALESCE(u.email, '')
		  FROM user_tokens t
		  LEFT JOIN users u ON u.id = t.user_id
		 WHERE t.id IN (`+strings.Join(holes, ",")+`)`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()

	type meta struct {
		name, tags, email string
		userID            int64
	}
	found := map[int64]meta{}
	for rows.Next() {
		var id int64
		var m meta
		if err := rows.Scan(&id, &m.name, &m.tags, &m.userID, &m.email); err != nil {
			return err
		}
		found[id] = m
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range spends {
		m, ok := found[s.TokenID]
		if ok {
			s.Name = m.name
			s.Tags = parseGroupsJSON(m.tags)
			s.UserID = m.userID
			s.Email = m.email
		}
		// token_id = 0 is the pre-v15 "unattributed" bucket, not a deleted key.
		s.Deleted = !ok && s.TokenID != 0
	}
	return nil
}

// tagsFromTokenSpend reproduces SpendByTag from the per-key rollup. SpendByTag
// INNER JOINs user_tokens, so a deleted key contributes to no tag — the check on
// Deleted keeps that behaviour.
func tagsFromTokenSpend(spends []*TokenSpend) []*TagSpend {
	idx := map[string]*TagSpend{}
	out := []*TagSpend{}
	for _, s := range spends {
		if s.Deleted || s.TokenID == 0 {
			continue
		}
		for _, tag := range s.Tags {
			t, ok := idx[tag]
			if !ok {
				t = &TagSpend{Tag: tag}
				idx[tag] = t
				out = append(out, t)
			}
			t.SpentUSD += s.SpentUSD
			t.ChargeEvents += s.ChargeEvents
			t.Tokens++
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SpentUSD != out[j].SpentUSD {
			return out[i].SpentUSD > out[j].SpentUSD
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}
