package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/saas/adapter"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
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
// the total. The one thing it must not do is bill twice, hence the idempotency
// key — reconciling the same window again is a no-op, so the command is safe to
// re-run after a partial failure, and safe to run when you are not sure whether
// someone already ran it.
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

// dropped is one (token, provider) pair's worth of unbilled traffic.
type dropped struct {
	ClientToken string
	Provider    string
	Requests    int64
	CostUSD     float64 // official upstream cost, pre-multiplier
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
}

func reconcileCharges(logDir, saasPath string, maxOverdraftUSD float64, from, to time.Time, apply bool) error {
	groups, err := readDroppedCharges(logDir, from, to)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		fmt.Printf("no dropped charges found between %s and %s\n",
			from.Format(time.RFC3339), to.Format(time.RFC3339))
		return nil
	}

	sdb, err := saasdb.Open(saasPath)
	if err != nil {
		return fmt.Errorf("open saas db: %w", err)
	}
	defer func() { _ = sdb.Close() }()

	ad := &adapter.Adapter{DB: sdb, MaxOverdraftUSD: maxOverdraftUSD}
	ctx := context.Background()

	settlements := make([]settlement, 0, len(groups))
	for _, g := range groups {
		s := settlement{dropped: g}
		info, ok := ad.LookupCtx(ctx, g.ClientToken)
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

	sort.Slice(settlements, func(i, j int) bool { return settlements[i].OwedUSD > settlements[j].OwedUSD })

	printSettlementReport(settlements, from, to, apply)
	if !apply {
		fmt.Println("\ndry run — nothing was written. Re-run with --apply to settle.")
		return nil
	}
	return applySettlements(ctx, sdb, settlements, from, to, maxOverdraftUSD)
}

// readDroppedCharges pulls the unbilled rows out of the request index.
//
// Read-only and query_only: the server normally has this database open, and
// the whole point of the 2026-08-22 incident is what happens when a second
// process disturbs a live SQLite file. mode=ro cannot create or checkpoint the
// WAL, and query_only refuses writes at the statement level, so this cannot be
// the thing that takes production down again.
func readDroppedCharges(logDir string, from, to time.Time) ([]dropped, error) {
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
		SELECT client_token, provider, COUNT(*), SUM(cost_usd),
		       SUM(input), SUM(output), SUM(cache_read), SUM(cache_create) + SUM(cache_create_1h)
		FROM req
		WHERE ts >= ? AND ts < ?
		  AND attempt_only = 0
		  AND cost_usd > 0
		  AND user_id = 0
		  AND client_token <> ''
		GROUP BY client_token, provider`,
		from.UnixNano(), to.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("query request index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []dropped
	for rows.Next() {
		var d dropped
		if err := rows.Scan(&d.ClientToken, &d.Provider, &d.Requests, &d.CostUSD,
			&d.Input, &d.Output, &d.CacheRead, &d.CacheCreate); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, d)
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
	fmt.Printf("%-14s %-10s %8s %12s %6s %12s  %s\n",
		"TOKEN", "PROVIDER", "REQS", "COST_USD", "MULT", "OWED_USD", "WORKSPACE")

	var totalCost, totalOwed float64
	var totalReqs int64
	var unresolved int
	for _, s := range ss {
		tok := s.ClientToken
		if len(tok) > 13 {
			tok = tok[:13] + "…"
		}
		ws := "ws" + strconv.FormatInt(s.WorkspaceID, 10) + " user" + strconv.FormatInt(s.UserID, 10)
		if s.Unresolved != "" {
			ws = "SKIPPED: " + s.Unresolved
			unresolved++
		}
		fmt.Printf("%-14s %-10s %8d %12.6f %6.3f %12.6f  %s\n",
			tok, s.Provider, s.Requests, s.CostUSD, s.Multiplier, s.OwedUSD, ws)
		totalCost += s.CostUSD
		totalOwed += s.OwedUSD
		totalReqs += s.Requests
	}
	fmt.Printf("\n%-14s %-10s %8d %12.6f %6s %12.6f\n", "TOTAL", "", totalReqs, totalCost, "", totalOwed)
	if unresolved > 0 {
		fmt.Printf("%d group(s) skipped because the token no longer resolves.\n", unresolved)
	}
}

func applySettlements(ctx context.Context, sdb *saasdb.DB, ss []settlement, from, to time.Time, maxOverdraftUSD float64) error {
	// One key per (window, token, provider). Re-running the same window is a
	// no-op rather than a second debit, which is what makes this command safe
	// to point at a window somebody may already have settled.
	keyPrefix := fmt.Sprintf("reconcile:%d-%d", from.Unix(), to.Unix())

	var settled, replayed, skipped int
	var settledUSD float64
	fmt.Println()
	for _, s := range ss {
		if s.Unresolved != "" || s.OwedUSD <= 0 {
			skipped++
			continue
		}
		key := fmt.Sprintf("%s:tok%d:%s", keyPrefix, s.TokenID, s.Provider)
		res, err := sdb.ChargeWorkspaceIdem(ctx, saasdb.IdemChargeReq{
			IdempotencyKey: key,
			Product:        "reconcile",
			WorkspaceID:    s.WorkspaceID,
			UserID:         s.UserID,
			AmountUSD:      s.OwedUSD,
			Ref:            fmt.Sprintf("token=%d model=%s-reconcile", s.TokenID, s.Provider),
			Note: fmt.Sprintf("backfilled %d request(s) whose charge was dropped between %s and %s (official $%.6f x %.3f)",
				s.Requests, from.Format(time.RFC3339), to.Format(time.RFC3339), s.CostUSD, s.Multiplier),
			MaxOverdraftUSD: maxOverdraftUSD,
			Meta: saasdb.ChargeMeta{
				TokenID:           s.TokenID,
				Model:             s.Provider + "-reconcile",
				InputTokens:       s.Input,
				OutputTokens:      s.Output,
				CacheReadTokens:   s.CacheRead,
				CacheCreateTokens: s.CacheCreate,
			},
		})
		if err != nil {
			return fmt.Errorf("settle %s: %w", key, err)
		}
		if res.TxID == 0 {
			// Clamped to nothing by the overdraft floor: the wallet is already
			// at its limit, the key stays unclaimed, and a later run can still
			// collect it if the customer tops up.
			fmt.Printf("  ws%-4d %-10s clamped to $0 by the overdraft floor (owed $%.6f)\n",
				s.WorkspaceID, s.Provider, s.OwedUSD)
			skipped++
			continue
		}
		verb := "settled"
		if res.ChargedUSD == 0 {
			verb = "already settled"
			replayed++
		} else {
			settled++
			settledUSD += res.ChargedUSD
		}
		fmt.Printf("  ws%-4d %-10s %s $%.6f (balance now $%.4f)\n",
			s.WorkspaceID, s.Provider, verb, res.ChargedUSD, res.NewBalanceUSD)
	}
	fmt.Printf("\nsettled %d group(s) totalling $%.6f", settled, settledUSD)
	if replayed > 0 {
		fmt.Printf("; %d already settled by an earlier run", replayed)
	}
	if skipped > 0 {
		fmt.Printf("; %d skipped", skipped)
	}
	fmt.Println()
	return nil
}
