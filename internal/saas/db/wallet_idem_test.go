package db

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// seedWorkspace returns a funded personal workspace to bill.
func seedWorkspace(t *testing.T, d *DB, email string, usd float64) (wsID, userID int64) {
	t.Helper()
	ctx := context.Background()
	userID = mkUser(t, d, email)
	if _, err := d.AddBalance(ctx, userID, TxKindTopup, usd, "seed", "", false); err != nil {
		t.Fatalf("seed balance: %v", err)
	}
	wsID, err := d.PersonalWorkspaceID(ctx, userID)
	if err != nil {
		t.Fatalf("personal workspace: %v", err)
	}
	return wsID, userID
}

func idemReq(wsID, userID int64, key string, amount float64) IdemChargeReq {
	return IdemChargeReq{
		IdempotencyKey:  key,
		Product:         "hypihub",
		WorkspaceID:     wsID,
		UserID:          userID,
		AmountUSD:       amount,
		Ref:             "job=job_x model=seedance-1-0-pro",
		MaxOverdraftUSD: 10,
		Meta:            ChargeMeta{Model: "seedance-1-0-pro"},
	}
}

// TestChargeWorkspaceIdemDebitsOnce is the whole point of migration v22: an
// HTTP biller that retries must not be billed twice.
func TestChargeWorkspaceIdemDebitsOnce(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	wsID, uid := seedWorkspace(t, d, "idem@example.com", 10)

	first, err := d.ChargeWorkspaceIdem(ctx, idemReq(wsID, uid, "hypihub:job:1", 2.5))
	if err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if first.Replayed {
		t.Fatal("first charge reported as a replay")
	}
	if !approxEq(first.ChargedUSD, 2.5) || !approxEq(first.NewBalanceUSD, 7.5) {
		t.Fatalf("first charge: charged=%v bal=%v want 2.5 / 7.5", first.ChargedUSD, first.NewBalanceUSD)
	}
	if first.TxID == 0 {
		t.Fatal("first charge wrote no ledger row")
	}

	second, err := d.ChargeWorkspaceIdem(ctx, idemReq(wsID, uid, "hypihub:job:1", 2.5))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed {
		t.Fatal("replay not flagged")
	}
	if second.TxID != first.TxID {
		t.Fatalf("replay returned a different row: %d vs %d", second.TxID, first.TxID)
	}
	if !approxEq(second.ChargedUSD, 2.5) {
		t.Fatalf("replay charged=%v want the original 2.5", second.ChargedUSD)
	}

	// The balance is the assertion that matters: one debit, not two.
	bal, err := d.GetWorkspaceBalance(ctx, wsID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if !approxEq(bal, 7.5) {
		t.Fatalf("double charge: balance %v want 7.5", bal)
	}
	if n := countIdem(t, d, "hypihub:job:1"); n != 1 {
		t.Fatalf("%d ledger rows for one key, want 1", n)
	}
}

// TestChargeWorkspaceIdemConcurrent drives N retries of the SAME key at once —
// the shape a client hitting a load-balanced pair of instances produces. The
// SELECT-then-INSERT is not atomic across connections, so this is what proves
// the partial UNIQUE index (not the SELECT) is doing the work.
func TestChargeWorkspaceIdemConcurrent(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	wsID, uid := seedWorkspace(t, d, "race@example.com", 100)

	const workers = 16
	var wg sync.WaitGroup
	results := make(chan IdemChargeResult, workers)
	errs := make(chan error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := d.ChargeWorkspaceIdem(ctx, idemReq(wsID, uid, "hypihub:job:race", 1.25))
			if err != nil {
				errs <- err
				return
			}
			results <- res
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent charge failed: %v", err)
	}
	var fresh, replays int
	var txID int64
	for res := range results {
		if res.Replayed {
			replays++
		} else {
			fresh++
		}
		if res.TxID == 0 {
			t.Fatal("concurrent result carried no tx id")
		}
		if txID == 0 {
			txID = res.TxID
		} else if res.TxID != txID {
			t.Fatalf("callers saw different rows for one key: %d vs %d", res.TxID, txID)
		}
		if !approxEq(res.ChargedUSD, 1.25) {
			t.Fatalf("charged=%v want 1.25", res.ChargedUSD)
		}
	}
	if fresh != 1 {
		t.Fatalf("%d callers performed a real debit, want exactly 1", fresh)
	}
	if replays != workers-1 {
		t.Fatalf("%d replays, want %d", replays, workers-1)
	}
	if n := countIdem(t, d, "hypihub:job:race"); n != 1 {
		t.Fatalf("%d ledger rows for one key, want 1", n)
	}
	bal, err := d.GetWorkspaceBalance(ctx, wsID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if !approxEq(bal, 98.75) {
		t.Fatalf("balance %v want 98.75 — more than one debit landed", bal)
	}
}

// TestCreditWorkspaceIdemRefundsOnce mirrors the charge case for the refund
// direction: positive amount, kind='refund', credited exactly once.
func TestCreditWorkspaceIdemRefundsOnce(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	wsID, uid := seedWorkspace(t, d, "refund@example.com", 5)

	if _, err := d.ChargeWorkspaceIdem(ctx, idemReq(wsID, uid, "hypihub:job:2", 2)); err != nil {
		t.Fatalf("charge: %v", err)
	}
	req := idemReq(wsID, uid, "hypihub:job:2:refund", 2)
	req.Note = "generation failed"

	first, err := d.CreditWorkspaceIdem(ctx, req)
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if first.Replayed || first.Clamped {
		t.Fatalf("unexpected flags on first refund: %+v", first)
	}
	if !approxEq(first.NewBalanceUSD, 5) {
		t.Fatalf("refund balance %v want 5", first.NewBalanceUSD)
	}

	second, err := d.CreditWorkspaceIdem(ctx, req)
	if err != nil {
		t.Fatalf("refund replay: %v", err)
	}
	if !second.Replayed || second.TxID != first.TxID {
		t.Fatalf("refund replay wrong: %+v (first %+v)", second, first)
	}
	bal, err := d.GetWorkspaceBalance(ctx, wsID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if !approxEq(bal, 5) {
		t.Fatalf("double refund: balance %v want 5", bal)
	}

	// The row must be a POSITIVE kind='refund' entry.
	var kind string
	var amount float64
	var product string
	row := d.QueryRowContext(ctx, `SELECT kind, amount_usd, product FROM wallet_tx WHERE idem_key = ?`, req.IdempotencyKey)
	if err := row.Scan(&kind, &amount, &product); err != nil {
		t.Fatalf("scan refund row: %v", err)
	}
	if kind != TxKindRefund || !approxEq(amount, 2) || product != "hypihub" {
		t.Fatalf("refund row = kind %q amount %v product %q", kind, amount, product)
	}
}

// TestChargeWorkspaceIdemClampsLikeTheInProcessPath pins that the HTTP path
// reuses the same overdraft math as ChargeWorkspaceWithFloor rather than a
// second copy of it.
func TestChargeWorkspaceIdemClampsLikeTheInProcessPath(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	wsID, uid := seedWorkspace(t, d, "clamp@example.com", 0.01)

	res, err := d.ChargeWorkspaceIdem(ctx, idemReq(wsID, uid, "hypihub:job:clamp", 50))
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	if !res.Clamped {
		t.Fatal("clamp not reported")
	}
	if !approxEq(res.ChargedUSD, 10.01) || !approxEq(res.NewBalanceUSD, -10) {
		t.Fatalf("charged=%v bal=%v want 10.01 / -10", res.ChargedUSD, res.NewBalanceUSD)
	}
	replay, err := d.ChargeWorkspaceIdem(ctx, idemReq(wsID, uid, "hypihub:job:clamp", 50))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.Replayed || !approxEq(replay.ChargedUSD, 10.01) || !replay.Clamped {
		t.Fatalf("clamped replay wrong: %+v", replay)
	}
}

func TestWalletIdemRejectsBadInput(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	wsID, uid := seedWorkspace(t, d, "bad@example.com", 10)

	cases := map[string]IdemChargeReq{
		"empty key":    idemReq(wsID, uid, "   ", 1),
		"zero amount":  idemReq(wsID, uid, "k1", 0),
		"negative":     idemReq(wsID, uid, "k2", -1),
		"over ceiling": idemReq(wsID, uid, "k3", maxIdemAmountUSD+0.01),
		"no workspace": idemReq(0, uid, "k4", 1),
	}
	for name, req := range cases {
		if _, err := d.ChargeWorkspaceIdem(ctx, req); err == nil {
			t.Errorf("%s: charge accepted, want rejection", name)
		}
		if _, err := d.CreditWorkspaceIdem(ctx, req); err == nil {
			t.Errorf("%s: credit accepted, want rejection", name)
		}
	}
	bal, err := d.GetWorkspaceBalance(ctx, wsID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if !approxEq(bal, 10) {
		t.Fatalf("rejected calls moved money: balance %v want 10", bal)
	}
}

// TestWalletIdemKeyCollisionAcrossWorkspaces: replaying a key against a
// different wallet must fail loudly, not hand back someone else's row.
func TestWalletIdemKeyCollisionAcrossWorkspaces(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	wsA, uidA := seedWorkspace(t, d, "a@example.com", 10)
	wsB, uidB := seedWorkspace(t, d, "b@example.com", 10)

	if _, err := d.ChargeWorkspaceIdem(ctx, idemReq(wsA, uidA, "shared-key", 1)); err != nil {
		t.Fatalf("charge A: %v", err)
	}
	_, err := d.ChargeWorkspaceIdem(ctx, idemReq(wsB, uidB, "shared-key", 1))
	if !errors.Is(err, ErrIdemConflict) {
		t.Fatalf("cross-workspace replay err = %v, want ErrIdemConflict", err)
	}
	// And a refund key must not be honoured as a charge.
	if _, err := d.CreditWorkspaceIdem(ctx, idemReq(wsA, uidA, "shared-key", 1)); !errors.Is(err, ErrIdemConflict) {
		t.Fatalf("kind mismatch err = %v, want ErrIdemConflict", err)
	}
	balB, err := d.GetWorkspaceBalance(ctx, wsB)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if !approxEq(balB, 10) {
		t.Fatalf("collision moved money in B: %v want 10", balB)
	}
}

// TestInProcessChargeLeavesIdemKeyEmpty guards the partial index: every legacy
// charge must stay OUT of it, or the second in-process charge ever written
// would violate uniqueness.
func TestInProcessChargeLeavesIdemKeyEmpty(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	wsID, uid := seedWorkspace(t, d, "legacy@example.com", 10)

	for i := 0; i < 3; i++ {
		if _, _, err := d.ChargeWorkspaceWithFloor(ctx, wsID, uid, TxKindCharge, 1, "token=1 model=m", "", 10, ChargeMeta{TokenID: 1, Model: "m"}); err != nil {
			t.Fatalf("in-process charge %d: %v", i, err)
		}
	}
	var n int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM wallet_tx WHERE kind = 'charge' AND idem_key = ''`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Fatalf("%d unkeyed charges, want 3", n)
	}
}

func countIdem(t *testing.T, d *DB, key string) int {
	t.Helper()
	var n int
	if err := d.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM wallet_tx WHERE idem_key = ?`, key).Scan(&n); err != nil {
		t.Fatalf("count idem: %v", err)
	}
	return n
}

// TestIsUniqueViolationMatchesDriver pins the string match in
// isUniqueViolation against what the driver actually produces. The insert-race
// fallback in walletIdem hangs on it: if the message ever changes, the loser of
// the race would surface a constraint error to an HTTP client that would then
// retry forever, instead of being told "already charged".
func TestIsUniqueViolationMatchesDriver(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	wsID, uid := seedWorkspace(t, d, "uniq@example.com", 10)

	if _, err := d.ChargeWorkspaceIdem(ctx, idemReq(wsID, uid, "dup-key", 1)); err != nil {
		t.Fatalf("charge: %v", err)
	}
	_, err := d.ExecContext(ctx,
		`INSERT INTO wallet_tx (user_id, workspace_id, kind, amount_usd, ref, note, created_at, idem_key)
		 VALUES (?, ?, 'charge', -1, '', '', 0, 'dup-key')`, uid, wsID)
	if err == nil {
		t.Fatal("duplicate idem_key was accepted — the v22 partial UNIQUE index is not enforcing")
	}
	if !isUniqueViolation(err) {
		t.Fatalf("isUniqueViolation did not recognise the driver error: %v", err)
	}

	// And the partial predicate must still allow many '' rows.
	if _, err := d.ExecContext(ctx,
		`INSERT INTO wallet_tx (user_id, workspace_id, kind, amount_usd, ref, note, created_at)
		 VALUES (?, ?, 'charge', -1, '', '', 0)`, uid, wsID); err != nil {
		t.Fatalf("unkeyed insert rejected: %v", err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO wallet_tx (user_id, workspace_id, kind, amount_usd, ref, note, created_at)
		 VALUES (?, ?, 'charge', -1, '', '', 0)`, uid, wsID); err != nil {
		t.Fatalf("second unkeyed insert rejected — the index is not partial: %v", err)
	}
}

// TestIdemLookupsUseThePartialIndex pins the query plan of both idempotency
// lookups to idx_wallet_tx_idem. The index is partial, and SQLite will not use
// it for a bare `idem_key = ?`; dropping the `<> ”` guard silently turns every
// proxy charge into a full ledger scan under the write lock (2026-09-03).
func TestIdemLookupsUseThePartialIndex(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	for name, q := range map[string]string{
		"lookup":  idemLookupSQL,
		"settled": idemSettledSQL("?"),
	} {
		rows, err := d.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q, "a")
		if err != nil {
			t.Fatalf("%s: explain: %v", name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatalf("%s: scan: %v", name, err)
			}
			plan = append(plan, detail)
		}
		_ = rows.Close()
		joined := strings.Join(plan, " | ")
		if !strings.Contains(joined, "idx_wallet_tx_idem") || strings.Contains(joined, "SCAN wallet_tx") {
			t.Fatalf("%s: plan does not use idx_wallet_tx_idem: %s", name, joined)
		}
	}
}
