package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/saas/adapter"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/server"
)

// runReconcileChargesCmd implements `<binary> reconcile-charges`.
//
// When the SaaS database is unwritable — the 2026-08-18 corruption, the
// 2026-08-22 unlinked-WAL outage — the proxy keeps serving. That is the right
// call: the upstream request has already been paid for by us, and refusing the
// customer's traffic on top of not billing it would turn a billing incident
// into an availability one. The cost is that every charge attempted in the
// window is simply lost; on 2026-08-22 that was 199 of them.
//
// The request log, which writes to a different database, records those requests
// anyway — and records them with a tell. proxy.go sets user_id and multiplier
// on the log row ONLY on the branch where Charge returned success; a failed
// charge leaves both at zero while cost_usd still carries what the request
// really cost. So "attempt_only = 0 AND cost_usd > 0 AND user_id = 0" is not a
// heuristic for a dropped charge, it is the exact record of one. Against the
// 2026-08-22 window it selects 199 rows; against the hour before it selects
// none out of 1295.
//
// Recovery therefore needs no guesswork and no estimate: re-derive each row's
// price through the same Lookup + MultiplierFor the live path uses, and settle
// it. The one thing it must not do is bill twice, hence the idempotency key:
// one per dropped ROW, derived from the row itself, so any window that covers
// a row a second time is a no-op for that row. The command is safe to re-run
// after a partial failure, safe to run when you are not sure whether someone
// already ran it, and safe to run over a window that overlaps an earlier one.
//
// That last property is new. The key used to name the (window, token, provider)
// group, which made the guarantee conditional on typing the same --from/--to
// byte for byte: two overlapping windows minted two keys for the rows they
// shared and billed them twice. Nothing in the command's own help said so.
//
// The index has no dropped rows before reconcileFloor. Until 2026-08-09 the
// proxy wrote billed_usd into cost_usd, and the rows now in the index for that
// period were backfilled from the wallet ledger, every one of them a charge
// that succeeded. A window reaching back further finds nothing to repair and
// says so, rather than implying the period was clean.
func runReconcileChargesCmd(args []string) {
	fs := flag.NewFlagSet("reconcile-charges", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "path to config file")
	from := fs.String("from", "", "window start, RFC3339 (e.g. 2026-08-22T12:55:18+08:00), inclusive")
	to := fs.String("to", "", "window end, RFC3339, exclusive")
	apply := fs.Bool("apply", false, "write the ledger rows; without it the command only reports")
	_ = fs.Parse(args)

	if *from == "" || *to == "" {
		fmt.Fprintln(os.Stderr, "reconcile-charges: --from and --to are required (RFC3339)")
		os.Exit(2)
	}
	fromT, err := time.Parse(time.RFC3339, *from)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reconcile-charges: --from: %v\n", err)
		os.Exit(2)
	}
	toT, err := time.Parse(time.RFC3339, *to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reconcile-charges: --to: %v\n", err)
		os.Exit(2)
	}
	if !toT.After(fromT) {
		fmt.Fprintln(os.Stderr, "reconcile-charges: --to must be after --from")
		os.Exit(2)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	if cfg.LogDir == "" {
		fmt.Fprintln(os.Stderr, "reconcile-charges: log_dir is not configured")
		os.Exit(2)
	}
	if !cfg.SaaS.Enabled || cfg.SaaS.DBPath == "" {
		fmt.Fprintln(os.Stderr, "reconcile-charges: saas is not enabled / saas.db_path is unset")
		os.Exit(2)
	}

	// Same floor the live adapter runs with, so a backfilled charge can never
	// drive a wallet somewhere an in-band charge would not have.
	var overdraft float64
	if cfg.SaaS.MaxOverdraftUSD != nil {
		overdraft = *cfg.SaaS.MaxOverdraftUSD
	}

	if err := reconcileCharges(cfg.LogDir, cfg.SaaS.DBPath, overdraft, fromT, toT, *apply); err != nil {
		fmt.Fprintf(os.Stderr, "reconcile-charges: %v\n", err)
		os.Exit(1)
	}
}

// reconcileFloor is the first instant the request index carries rows from
// which a dropped charge can be told apart from a settled one (commit d73215e
// split cost_usd from billed_usd; before it the two were the same number, and
// the index for the earlier period is a ledger backfill — successful charges
// only). Windows are clamped to it.
var reconcileFloor = time.Date(2026, time.August, 9, 12, 13, 10, 0, time.UTC) // 20:13:10 HKT

// droppedRow is one request whose charge was not written.
type droppedRow struct {
	TS          int64 // UnixNano, as stored
	ClientToken string
	Provider    string
	Model       string
	CostUSD     float64 // official upstream cost, pre-multiplier
	Input       int64
	Output      int64
	CacheRead   int64
	CacheCreate int64
}

// idemKey names this row's settlement. Content-derived, so it is the same key
// from any window that covers the row, and stable across an index rebuild
// (req.id is not: it is assigned on ingest).
func (r droppedRow) idemKey() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%s|%.8f|%d|%d|%d|%d",
		r.TS, r.ClientToken, r.Provider, r.Model, r.CostUSD, r.Input, r.Output, r.CacheRead, r.CacheCreate)))
	return "reconcile:row:" + hex.EncodeToString(h[:16])
}

// dropped is one (token, provider) pair's worth of unbilled traffic — the unit
// the report is read in. Settlement happens per row.
type dropped struct {
	ClientToken string
	Provider    string
	Rows        []droppedRow
	Requests    int64
	CostUSD     float64
	Input       int64
	Output      int64
	CacheRead   int64
	CacheCreate int64
}

// settlement is a dropped group after its price has been re-derived.
type settlement struct {
	dropped
	TokenID     int64
	UserID      int64
	WorkspaceID int64
	Multiplier  float64
	OwedUSD     float64
	// Unresolved records why a group could not be priced (deleted token,
	// deleted workspace). Such rows are reported and skipped, never guessed at.
	Unresolved string
	// SettledUSD / SettledRows is what an earlier run already put on the
	// ledger for this group's rows; this run will not move it again.
	SettledUSD  float64
	SettledRows int
}

func reconcileCharges(logDir, saasPath string, maxOverdraftUSD float64, from, to time.Time, apply bool) error {
	if from.Before(reconcileFloor) {
		fmt.Printf("note: the request index cannot show dropped charges before %s; window start moved up from %s\n",
			reconcileFloor.Format(time.RFC3339), from.Format(time.RFC3339))
		from = reconcileFloor
		if !to.After(from) {
			fmt.Println("nothing to do: the whole window predates the index's first live row")
			return nil
		}
	}
	rows, err := readDroppedCharges(logDir, from, to)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Printf("no dropped charges found between %s and %s\n",
			from.Format(time.RFC3339), to.Format(time.RFC3339))
		return nil
	}
	groups := groupDropped(rows)

	// A dry run reports; it has no reason to hold a writable handle on a
	// database the server is using. See OpenReadOnly — attaching read-write to
	// a live SQLite file is the thing that caused the outage this command
	// exists to repair, so the reporting path refuses to do it.
	open := saasdb.OpenReadOnly
	if apply {
		open = saasdb.Open
	}
	sdb, err := open(saasPath)
	if err != nil {
		return fmt.Errorf("open saas db: %w", err)
	}
	defer func() { _ = sdb.Close() }()

	ad := &adapter.Adapter{DB: sdb, MaxOverdraftUSD: maxOverdraftUSD}
	ctx := context.Background()

	// req.client_token holds the MASKED token, not the secret, so the secret
	// has to be recovered before anything can be looked up. Masking is one-way;
	// the way back is to mask every known token and match.
	unmask, err := buildUnmasker(ctx, sdb)
	if err != nil {
		return fmt.Errorf("map masked tokens: %w", err)
	}

	settlements := make([]settlement, 0, len(groups))
	for _, g := range groups {
		s := settlement{dropped: g}
		secret, why := unmask(g.ClientToken)
		if why != "" {
			s.Unresolved = why
			settlements = append(settlements, s)
			continue
		}
		info, ok := ad.LookupCtx(ctx, secret)
		if !ok {
			s.Unresolved = "token no longer resolves (deleted?)"
			settlements = append(settlements, s)
			continue
		}
		s.TokenID = info.TokenID
		s.UserID = info.UserID
		s.WorkspaceID = info.WorkspaceID
		s.Multiplier = ad.MultiplierFor(info, g.Provider)
		s.OwedUSD = g.CostUSD * s.Multiplier
		settlements = append(settlements, s)
	}

	// Nothing in the request log records that a dropped charge was later
	// recovered, so ask the ledger before reporting: an already-repaired window
	// must not look like a fresh outage.
	var keys []string
	for _, s := range settlements {
		if s.Unresolved != "" {
			continue
		}
		for _, r := range s.Rows {
			keys = append(keys, r.idemKey())
		}
	}
	settled, err := sdb.SettledIdemKeys(ctx, keys)
	if err != nil {
		return fmt.Errorf("check settled keys: %w", err)
	}
	for i := range settlements {
		if settlements[i].Unresolved != "" {
			continue
		}
		for _, r := range settlements[i].Rows {
			if amt, ok := settled[r.idemKey()]; ok {
				settlements[i].SettledUSD += amt
				settlements[i].SettledRows++
			}
		}
	}

	sort.Slice(settlements, func(i, j int) bool { return settlements[i].OwedUSD > settlements[j].OwedUSD })

	printSettlementReport(settlements, from, to, apply)
	if !apply {
		fmt.Println("\ndry run — nothing was written. Re-run with --apply to settle.")
		return nil
	}
	return applySettlements(ctx, sdb, settlements, maxOverdraftUSD)
}

// groupDropped folds rows into (token, provider) groups, in first-seen order.
func groupDropped(rows []droppedRow) []dropped {
	idx := make(map[string]int)
	var out []dropped
	for _, r := range rows {
		k := r.ClientToken + "\x00" + r.Provider
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, dropped{ClientToken: r.ClientToken, Provider: r.Provider})
		}
		g := &out[i]
		g.Rows = append(g.Rows, r)
		g.Requests++
		g.CostUSD += r.CostUSD
		g.Input += r.Input
		g.Output += r.Output
		g.CacheRead += r.CacheRead
		g.CacheCreate += r.CacheCreate
	}
	return out
}

// buildUnmasker returns a function mapping a masked client_token back to its
// secret.
//
// Ambiguity is reported rather than resolved. Two live tokens sharing a first-6
// and last-4 is unlikely but not impossible, and picking either one would bill
// a real customer for another customer's traffic — the one error this command
// must never make. Such a group is surfaced for a human instead.
func buildUnmasker(ctx context.Context, sdb *saasdb.DB) (func(masked string) (secret, why string), error) {
	secrets, err := sdb.AllTokenSecrets(ctx)
	if err != nil {
		return nil, err
	}
	byMask := make(map[string]string, len(secrets))
	ambiguous := make(map[string]bool)
	for _, sec := range secrets {
		m := server.MaskClientToken(sec)
		if _, dup := byMask[m]; dup {
			ambiguous[m] = true
			continue
		}
		byMask[m] = sec
	}
	return func(masked string) (string, string) {
		if ambiguous[masked] {
			return "", "masked value matches more than one live token"
		}
		sec, ok := byMask[masked]
		if !ok {
			return "", "token no longer resolves (deleted?)"
		}
		return sec, ""
	}, nil
}

// readDroppedCharges pulls the unbilled rows out of the request index, one per
// request, in log order.
//
// The index is opened strictly read-only: the whole point of the 2026-08-22
// incident is what happens when a second process disturbs a live SQLite file.
// mode=ro cannot create or checkpoint the WAL, and query_only refuses writes at
// the statement level, so this cannot be the thing that takes production down
// again.
//
// cache_create is read alone. It used to be summed with cache_create_1h, but
// cc-core defines CacheCreate1hTokens as the SUBSET of CacheCreateTokens
// written at the 1h TTL (usage.Counts), so adding the two counted every 1h
// write twice on the ledger row. Money was never affected — the amount comes
// from cost_usd, not from the token columns — but the ledger's token meta
// would have disagreed with the request log for any row carrying a 1h write.
func readDroppedCharges(logDir string, from, to time.Time) ([]droppedRow, error) {
	path := logDir + "/requests.db"
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("request index %s: %w", path, err)
	}
	rdb, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, fmt.Errorf("open request index: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	// req.ts is UnixNano, not Unix seconds.
	rows, err := rdb.Query(`
		SELECT ts, client_token, provider, model, cost_usd,
		       input, output, cache_read, cache_create
		FROM req
		WHERE ts >= ? AND ts < ?
		  AND attempt_only = 0
		  AND cost_usd > 0
		  AND user_id = 0
		  AND client_token <> ''
		ORDER BY ts, id`,
		from.UnixNano(), to.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("query request index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []droppedRow
	for rows.Next() {
		var r droppedRow
		if err := rows.Scan(&r.TS, &r.ClientToken, &r.Provider, &r.Model, &r.CostUSD,
			&r.Input, &r.Output, &r.CacheRead, &r.CacheCreate); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func printSettlementReport(ss []settlement, from, to time.Time, apply bool) {
	mode := "DRY RUN"
	if apply {
		mode = "APPLYING"
	}
	fmt.Printf("%s — dropped charges between %s and %s\n\n", mode,
		from.Format(time.RFC3339), to.Format(time.RFC3339))
	fmt.Printf("%-14s %-10s %8s %12s %6s %12s %12s  %s\n",
		"TOKEN", "PROVIDER", "REQS", "COST_USD", "MULT", "OWED_USD", "SETTLED", "WORKSPACE")

	var totalCost, totalOwed, totalSettled float64
	var totalReqs int64
	var unresolved, partlySettled int
	for _, s := range ss {
		tok := s.ClientToken
		if len(tok) > 13 {
			tok = tok[:13] + "…"
		}
		ws := "ws" + strconv.FormatInt(s.WorkspaceID, 10) + " user" + strconv.FormatInt(s.UserID, 10)
		switch {
		case s.Unresolved != "":
			ws = "SKIPPED: " + s.Unresolved
			unresolved++
		case s.SettledRows > 0:
			ws += fmt.Sprintf("  [%d/%d row(s) already settled]", s.SettledRows, len(s.Rows))
			partlySettled++
		}
		fmt.Printf("%-14s %-10s %8d %12.6f %6.3f %12.6f %12.6f  %s\n",
			tok, s.Provider, s.Requests, s.CostUSD, s.Multiplier, s.OwedUSD, s.SettledUSD, ws)
		totalCost += s.CostUSD
		totalOwed += s.OwedUSD
		totalSettled += s.SettledUSD
		totalReqs += s.Requests
	}
	fmt.Printf("\n%-14s %-10s %8d %12.6f %6s %12.6f %12.6f\n", "TOTAL", "", totalReqs, totalCost, "", totalOwed, totalSettled)
	if unresolved > 0 {
		fmt.Printf("%d group(s) skipped because the token could not be resolved.\n", unresolved)
	}
	if partlySettled > 0 {
		fmt.Printf("%d group(s) have rows an earlier run already settled; re-running moves no money for those rows.\n", partlySettled)
	}
}

func applySettlements(ctx context.Context, sdb *saasdb.DB, ss []settlement, maxOverdraftUSD float64) error {
	var settled, replayed, clamped, skipped int
	var settledUSD float64
	fmt.Println()
	for _, s := range ss {
		if s.Unresolved != "" || s.OwedUSD <= 0 {
			skipped++
			continue
		}
		var groupUSD float64
		var groupSettled, groupReplayed, groupClamped int
		var lastBal float64
		for _, r := range s.Rows {
			owed := r.CostUSD * s.Multiplier
			if owed <= 0 {
				continue
			}
			at := time.Unix(0, r.TS).UTC()
			res, err := sdb.ChargeWorkspaceIdem(ctx, saasdb.IdemChargeReq{
				IdempotencyKey: r.idemKey(),
				Product:        "reconcile",
				WorkspaceID:    s.WorkspaceID,
				UserID:         s.UserID,
				AmountUSD:      owed,
				Ref:            fmt.Sprintf("token=%d model=%s", s.TokenID, r.Model),
				Note: fmt.Sprintf("backfilled: charge for the %s request at %s was dropped (official $%.6f x %.3f)",
					r.Provider, at.Format(time.RFC3339), r.CostUSD, s.Multiplier),
				MaxOverdraftUSD: maxOverdraftUSD,
				Meta: saasdb.ChargeMeta{
					TokenID:           s.TokenID,
					Model:             r.Model,
					InputTokens:       r.Input,
					OutputTokens:      r.Output,
					CacheReadTokens:   r.CacheRead,
					CacheCreateTokens: r.CacheCreate,
				},
			})
			if err != nil {
				return fmt.Errorf("settle %s: %w", r.idemKey(), err)
			}
			lastBal = res.NewBalanceUSD
			switch {
			case res.TxID == 0:
				// Clamped to nothing by the overdraft floor: the wallet is
				// already at its limit, the key stays unclaimed, and a later
				// run can still collect it if the customer tops up.
				groupClamped++
			case res.Replayed:
				groupReplayed++
			default:
				groupSettled++
				groupUSD += res.ChargedUSD
			}
		}
		fmt.Printf("  ws%-4d %-10s settled %d row(s) $%.6f", s.WorkspaceID, s.Provider, groupSettled, groupUSD)
		if groupReplayed > 0 {
			fmt.Printf("; %d already settled", groupReplayed)
		}
		if groupClamped > 0 {
			fmt.Printf("; %d clamped to $0 by the overdraft floor", groupClamped)
		}
		fmt.Printf(" (balance now $%.4f)\n", lastBal)
		settled += groupSettled
		replayed += groupReplayed
		clamped += groupClamped
		settledUSD += groupUSD
	}
	fmt.Printf("\nsettled %d row(s) totalling $%.6f", settled, settledUSD)
	if replayed > 0 {
		fmt.Printf("; %d already settled by an earlier run", replayed)
	}
	if clamped > 0 {
		fmt.Printf("; %d clamped", clamped)
	}
	if skipped > 0 {
		fmt.Printf("; %d group(s) skipped", skipped)
	}
	fmt.Println()
	return nil
}
