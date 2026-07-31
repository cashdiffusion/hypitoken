package db

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// TestConcurrentChargesDoNotReturnBusy is a regression test for the production
// incident where ~29% of all charge attempts failed with
// "database is locked (5) (SQLITE_BUSY)" — 7.8k lost charges in 24h, requests
// served and never billed.
//
// The cause was Go's default BEGIN DEFERRED. Charge reads the balance and then
// writes it, so a deferred transaction has to upgrade a read lock to a write
// lock; when another connection already holds the write lock SQLite fails that
// upgrade with SQLITE_BUSY *immediately*, deliberately ignoring busy_timeout
// (waiting could deadlock two readers both trying to upgrade). No amount of
// busy_timeout tuning fixes it — only opening the transaction as BEGIN
// IMMEDIATE, which is what _txlock=immediate in Open's DSN now does.
//
// Reverting that DSN flag makes this test fail, which is the point: the symptom
// is invisible at low concurrency and silent in production (a warning log, no
// retry), so the guard has to be a concurrent one.
func TestConcurrentChargesDoNotReturnBusy(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "busy@example.com")

	// Fund the wallet well above the total spend so nothing fails for balance
	// reasons and any error we see is a locking error.
	if _, err := d.AddBalance(ctx, uid, "topup", 1000, "seed", "", false); err != nil {
		t.Fatalf("seed balance: %v", err)
	}

	const (
		workers          = 8 // matches SetMaxOpenConns, so every conn contends
		chargesPerWorker = 25
	)

	var wg sync.WaitGroup
	errCh := make(chan error, workers*chargesPerWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < chargesPerWorker; i++ {
				if _, _, err := d.ChargeWithFloor(ctx, uid, "usage", 0.01, "ref", "", 0, ChargeMeta{}); err != nil {
					errCh <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)

	var busy, other int
	var sample string
	for err := range errCh {
		if strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked") {
			busy++
		} else {
			other++
		}
		if sample == "" {
			sample = err.Error()
		}
	}
	if busy > 0 {
		t.Errorf("%d/%d concurrent charges failed with SQLITE_BUSY — deferred-transaction "+
			"upgrade contention is back (check _txlock=immediate in Open's DSN); sample: %s",
			busy, workers*chargesPerWorker, sample)
	}
	if other > 0 {
		t.Errorf("%d concurrent charges failed for a non-locking reason; sample: %s", other, sample)
	}

	// Every charge must also have landed: a silently-dropped charge is the exact
	// production symptom, so assert the ledger balances rather than only that no
	// error was returned.
	var got float64
	if err := d.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(-amount_usd),0) FROM wallet_tx WHERE user_id=? AND kind='usage'`,
		uid).Scan(&got); err != nil {
		t.Fatalf("sum charges: %v", err)
	}
	want := 0.01 * float64(workers*chargesPerWorker)
	if diff := got - want; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("charged total = %.6f, want %.6f", got, want)
	}
}
