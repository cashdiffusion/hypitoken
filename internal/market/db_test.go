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

	// Two reservations fill the stock.
	for i := 0; i < 2; i++ {
		if err := db.CreateOrderReserving(ctx, mkOrder(p, OrderPending)); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	// Third must be sold out.
	if err := db.CreateOrderReserving(ctx, mkOrder(p, OrderPending)); !errors.Is(err, ErrSoldOut) {
		t.Fatalf("expected ErrSoldOut, got %v", err)
	}

	got, err := db.GetProduct(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Reserved != 2 || got.Available != 0 || !got.SoldOut() {
		t.Fatalf("reserved=%d available=%d soldout=%v", got.Reserved, got.Available, got.SoldOut())
	}
}

func TestExpiryFreesUnit(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	p := mkProduct(t, db, 100, 0.10, 1)

	o := mkOrder(p, OrderPending)
	if err := db.CreateOrderReserving(ctx, o); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	// Sold out while pending.
	if pr, _ := db.GetProduct(ctx, p.ID); !pr.SoldOut() {
		t.Fatal("expected sold out while pending")
	}
	// Expire everything created before "now+1m".
	if _, err := db.ExpirePendingBefore(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("expire: %v", err)
	}
	// Unit freed.
	if pr, _ := db.GetProduct(ctx, p.ID); pr.SoldOut() {
		t.Fatal("expected available after expiry")
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
