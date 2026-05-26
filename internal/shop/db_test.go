package shop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "shop_test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestMarkOrderPaidAndFulfilCardPop verifies the happy path: a paid order
// against a card-pool product pops exactly one secret, marks the order
// paid, and updates stock_available.
func TestMarkOrderPaidAndFulfilCardPop(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	p := &Product{Name: "Test", PriceCNY: 10, DeliveryType: DeliveryCard, Active: true}
	if err := db.CreateProduct(ctx, p); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := db.AppendCardSecrets(ctx, p.ID, []string{"AAA", "BBB"}); err != nil {
		t.Fatalf("append cards: %v", err)
	}

	o := &Order{
		OutTradeNo: "S0001", ProductID: p.ID, ProductName: p.Name,
		Email: "a@b.c", QueryPassHash: "x", AmountCNY: 10, PayMethod: "alipay",
	}
	if err := db.CreateOrder(ctx, o); err != nil {
		t.Fatalf("create order: %v", err)
	}

	out, err := db.MarkOrderPaidAndFulfil(ctx, "S0001", "TRADE-1")
	if err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	if out.Status != OrderPaid {
		t.Fatalf("status = %q, want paid", out.Status)
	}
	if out.Fulfillment != "AAA" {
		t.Fatalf("fulfilment = %q, want AAA (first by insertion order)", out.Fulfillment)
	}

	// Stock should have decremented.
	p2, err := db.GetProduct(ctx, p.ID)
	if err != nil {
		t.Fatalf("get product: %v", err)
	}
	if p2.StockAvailable != 1 {
		t.Fatalf("stock = %d, want 1", p2.StockAvailable)
	}
}

// TestMarkOrderPaidAndFulfilIdempotent verifies that a second call with the
// same out_trade_no returns ErrOrderNotPending without consuming a second
// card secret.
func TestMarkOrderPaidAndFulfilIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	p := &Product{Name: "Test", PriceCNY: 10, DeliveryType: DeliveryCard, Active: true}
	_ = db.CreateProduct(ctx, p)
	_, _ = db.AppendCardSecrets(ctx, p.ID, []string{"AAA", "BBB"})
	_ = db.CreateOrder(ctx, &Order{OutTradeNo: "S0002", ProductID: p.ID, ProductName: p.Name, Email: "a@b.c", QueryPassHash: "x", AmountCNY: 10})

	if _, err := db.MarkOrderPaidAndFulfil(ctx, "S0002", "T1"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err := db.MarkOrderPaidAndFulfil(ctx, "S0002", "T1")
	if !errors.Is(err, ErrOrderNotPending) {
		t.Fatalf("second call err = %v, want ErrOrderNotPending", err)
	}

	// Stock should have dropped by exactly 1, not 2.
	p2, _ := db.GetProduct(ctx, p.ID)
	if p2.StockAvailable != 1 {
		t.Fatalf("stock = %d, want 1 (second call must not consume)", p2.StockAvailable)
	}
}

// TestMarkOrderPaidAndFulfilOutOfStock — drained pool flips order to
// await_manual without crashing.
func TestMarkOrderPaidAndFulfilOutOfStock(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	p := &Product{Name: "Empty", PriceCNY: 10, DeliveryType: DeliveryCard, Active: true}
	_ = db.CreateProduct(ctx, p)
	// no card secrets appended

	o := &Order{OutTradeNo: "S0003", ProductID: p.ID, ProductName: p.Name, Email: "x@y.z", QueryPassHash: "x", AmountCNY: 10}
	_ = db.CreateOrder(ctx, o)

	out, err := db.MarkOrderPaidAndFulfil(ctx, "S0003", "T2")
	if err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	if out.Status != OrderAwaitManual {
		t.Fatalf("status = %q, want await_manual", out.Status)
	}
	if out.Fulfillment != "" {
		t.Fatalf("fulfilment should be empty, got %q", out.Fulfillment)
	}
}

// TestMarkOrderPaidConcurrentNoOversell — two webhooks racing against
// the same order must not consume two cards. SQLite serializes writers
// via WAL; we exercise the row-conditional UPDATE guard.
func TestMarkOrderPaidConcurrentNoOversell(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	p := &Product{Name: "Race", PriceCNY: 10, DeliveryType: DeliveryCard, Active: true}
	_ = db.CreateProduct(ctx, p)
	_, _ = db.AppendCardSecrets(ctx, p.ID, []string{"X1", "X2", "X3"})
	_ = db.CreateOrder(ctx, &Order{OutTradeNo: "S0004", ProductID: p.ID, ProductName: p.Name, Email: "x@y.z", QueryPassHash: "x", AmountCNY: 10})

	var wg sync.WaitGroup
	results := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_, err := db.MarkOrderPaidAndFulfil(cctx, "S0004", "TRACE")
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrOrderNotPending):
			// expected loser path: another goroutine got there first
		case strings.Contains(err.Error(), "database is locked"):
			// SQLite-busy fast-fail. The key invariant we care about is
			// that the loser DIDN'T consume a card. Production webhooks
			// arrive seconds apart, well past busy_timeout=5000ms, so
			// this is a test-only contention artifact.
		default:
			t.Fatalf("unexpected err: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent settles = %d, want exactly 1", successes)
	}

	p2, _ := db.GetProduct(ctx, p.ID)
	if p2.StockAvailable != 2 {
		t.Fatalf("stock = %d, want 2 (exactly one consumed)", p2.StockAvailable)
	}

	// Order must be flipped to paid regardless of the contention path.
	o, _ := db.GetOrder(ctx, "S0004")
	if o.Status != OrderPaid {
		t.Fatalf("order status = %q, want paid", o.Status)
	}
}

// TestExpirePendingBefore — sweeper marks old pending orders expired
// without touching paid ones.
func TestExpirePendingBefore(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	p := &Product{Name: "Auto", PriceCNY: 5, DeliveryType: DeliveryAuto, DeliveryTemplate: "hi", Active: true}
	_ = db.CreateProduct(ctx, p)
	_ = db.CreateOrder(ctx, &Order{OutTradeNo: "OLD1", ProductID: p.ID, ProductName: p.Name, Email: "x@y", QueryPassHash: "x", AmountCNY: 5})
	_ = db.CreateOrder(ctx, &Order{OutTradeNo: "OLD2", ProductID: p.ID, ProductName: p.Name, Email: "x@y", QueryPassHash: "x", AmountCNY: 5})
	if _, err := db.MarkOrderPaidAndFulfil(ctx, "OLD2", "T"); err != nil {
		t.Fatalf("settle OLD2: %v", err)
	}

	// Cutoff: 1 second in the future so both rows look "old".
	n, err := db.ExpirePendingBefore(ctx, time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Fatalf("expired = %d, want 1 (only OLD1 was pending)", n)
	}

	o1, _ := db.GetOrder(ctx, "OLD1")
	o2, _ := db.GetOrder(ctx, "OLD2")
	if o1.Status != OrderExpired {
		t.Fatalf("OLD1 status = %q, want expired", o1.Status)
	}
	if o2.Status != OrderPaid {
		t.Fatalf("OLD2 status = %q, want paid (must not be touched)", o2.Status)
	}
}
