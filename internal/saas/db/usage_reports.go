package db

import (
	"context"
	"fmt"
	"slices"
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
