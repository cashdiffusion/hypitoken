package market

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "market_test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mkProduct(t *testing.T, db *DB, price, ratio float64, qty int) *Product {
	t.Helper()
	p := &Product{Name: "测试商品", Price: price, DepositRatio: ratio, Quantity: qty, Active: true, Images: []string{}}
	if err := db.CreateProduct(context.Background(), p); err != nil {
		t.Fatalf("create product: %v", err)
	}
	return p
}

func mkOrder(p *Product, status string) *Order {
	return &Order{
		OutTradeNo:    newOrderID(),
		ProductID:     p.ID,
		ProductName:   p.Name,
		Price:         p.Price,
		DepositAmount: p.DepositAmount(),
		DepositRatio:  p.DepositRatio,
		Status:        status,
		FulfilMethod:  FulfilPickup,
		Contact:       "wx:test",
	}
}

func TestDepositAmount(t *testing.T) {
	cases := []struct {
		price, ratio, want float64
	}{
		{100, 0.10, 10.00},
		{99.9, 0.10, 9.99},
		{33.33, 0.10, 3.33},
		{50, 0.2, 10.00},
		{19.99, 0.15, 3.00}, // 2.9985 -> 3.00
	}
	for _, c := range cases {
		p := &Product{Price: c.price, DepositRatio: c.ratio}
		if got := p.DepositAmount(); got != c.want {
			t.Errorf("deposit(%.2f, %.2f) = %.2f, want %.2f", c.price, c.ratio, got, c.want)
		}
	}
}

func TestReserveAndSoldOut(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	p := mkProduct(t, db, 100, 0.10, 2) // 2 units

	// Pending (unpaid) orders must NOT reserve — the product stays available
	// no matter how many checkouts start without paying.
	o1 := mkOrder(p, OrderPending)
	if err := db.CreateOrderReserving(ctx, o1); err != nil {
		t.Fatalf("reserve1: %v", err)
	}
	o2 := mkOrder(p, OrderPending)
	if err := db.CreateOrderReserving(ctx, o2); err != nil {
		t.Fatalf("reserve2: %v", err)
	}
	if pr, _ := db.GetProduct(ctx, p.ID); pr.Reserved != 0 || pr.SoldOut() {
		t.Fatalf("pending must not reserve: reserved=%d soldout=%v", pr.Reserved, pr.SoldOut())
	}

	// Real, paid deposits claim the units.
	if _, err := db.MarkPaid(ctx, o1.OutTradeNo, "t1"); err != nil {
		t.Fatalf("pay1: %v", err)
	}
	if _, err := db.MarkPaid(ctx, o2.OutTradeNo, "t2"); err != nil {
		t.Fatalf("pay2: %v", err)
	}
	got, err := db.GetProduct(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Reserved != 2 || got.Available != 0 || !got.SoldOut() {
		t.Fatalf("after paying 2: reserved=%d available=%d soldout=%v", got.Reserved, got.Available, got.SoldOut())
	}
	// Only now — real money filled the stock — is a new checkout rejected.
	if err := db.CreateOrderReserving(ctx, mkOrder(p, OrderPending)); !errors.Is(err, ErrSoldOut) {
		t.Fatalf("expected ErrSoldOut once paid out, got %v", err)
	}
}

func TestExpiryFreesUnit(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	p := mkProduct(t, db, 100, 0.10, 2)

	// A pending (unpaid) order does not sell the product out.
	o1 := mkOrder(p, OrderPending)
	if err := db.CreateOrderReserving(ctx, o1); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if pr, _ := db.GetProduct(ctx, p.ID); pr.SoldOut() {
		t.Fatal("pending order must not sell out the product")
	}
	// Paying one unit reserves it (real money only).
	if _, err := db.MarkPaid(ctx, o1.OutTradeNo, "tp"); err != nil {
		t.Fatalf("pay: %v", err)
	}
	// Expiry tidies an abandoned pending order without it ever holding stock.
	o2 := mkOrder(p, OrderPending)
	if err := db.CreateOrderReserving(ctx, o2); err != nil {
		t.Fatalf("reserve2: %v", err)
	}
	n, err := db.ExpirePendingBefore(ctx, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pending expired, got %d", n)
	}
	if got, _ := db.GetOrder(ctx, o2.OutTradeNo); got.Status != OrderExpired {
		t.Fatalf("expected expired, got %s", got.Status)
	}
	// Only the paid unit still holds; one unit remains available.
	if pr, _ := db.GetProduct(ctx, p.ID); pr.Reserved != 1 || pr.Available != 1 {
		t.Fatalf("after expiry: reserved=%d available=%d", pr.Reserved, pr.Available)
	}
}

func TestMarkPaidIdempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	p := mkProduct(t, db, 100, 0.10, 1)
	o := mkOrder(p, OrderPending)
	if err := db.CreateOrderReserving(ctx, o); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := db.MarkPaid(ctx, o.OutTradeNo, "T123"); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	// Second confirmation is a no-op signal.
	if _, err := db.MarkPaid(ctx, o.OutTradeNo, "T123"); !errors.Is(err, ErrOrderNotPending) {
		t.Fatalf("expected ErrOrderNotPending on re-pay, got %v", err)
	}
	got, _ := db.GetOrder(ctx, o.OutTradeNo)
	if got.Status != OrderPaid || got.TradeNo != "T123" || got.PaidAt.IsZero() {
		t.Fatalf("bad order after pay: %+v", got)
	}
}

func TestPaidStillReservesAfterExpirySweep(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	p := mkProduct(t, db, 100, 0.10, 1)
	o := mkOrder(p, OrderPending)
	if err := db.CreateOrderReserving(ctx, o); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := db.MarkPaid(ctx, o.OutTradeNo, "T1"); err != nil {
		t.Fatalf("pay: %v", err)
	}
	// A sweep must NOT free a paid unit.
	if _, err := db.ExpirePendingBefore(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if pr, _ := db.GetProduct(ctx, p.ID); !pr.SoldOut() {
		t.Fatal("paid unit must stay reserved after sweep")
	}
}
