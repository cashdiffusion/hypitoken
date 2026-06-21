package db

import (
	"context"
	"math"
	"testing"
)

func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestChargeWithFloorClampsToOverdraft(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "floor@example.com")

	// Seed a near-zero balance, then bill far more than it covers.
	if _, err := d.AddBalance(ctx, uid, TxKindAdjust, 0.01, "seed", "", true); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A $50 charge against $0.01 with a $10 overdraft floor must clamp so the
	// wallet rests at exactly -$10, charging only $10.01.
	newBal, charged, err := d.ChargeWithFloor(ctx, uid, TxKindCharge, 50, "big", "", 10)
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	if !approxEq(newBal, -10) {
		t.Fatalf("balance not clamped to floor: got %v want -10", newBal)
	}
	if !approxEq(charged, 10.01) {
		t.Fatalf("charged not clamped: got %v want 10.01", charged)
	}

	// Already at the floor — a further charge takes nothing and never deepens.
	newBal, charged, err = d.ChargeWithFloor(ctx, uid, TxKindCharge, 5, "more", "", 10)
	if err != nil {
		t.Fatalf("charge2: %v", err)
	}
	if !approxEq(charged, 0) {
		t.Fatalf("over-floor charge should be 0: got %v", charged)
	}
	if !approxEq(newBal, -10) {
		t.Fatalf("balance moved past floor: got %v want -10", newBal)
	}
}

func TestChargeWithFloorNoClampWithinBudget(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "ok@example.com")
	if _, err := d.AddBalance(ctx, uid, TxKindAdjust, 5, "seed", "", true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// $3 charge against $5 with a $10 floor: full charge, balance $2.
	newBal, charged, err := d.ChargeWithFloor(ctx, uid, TxKindCharge, 3, "ok", "", 10)
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	if !approxEq(charged, 3) || !approxEq(newBal, 2) {
		t.Fatalf("unexpected: charged=%v bal=%v", charged, newBal)
	}
}

func TestChargeWithFloorDisabled(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "unbounded@example.com")
	// floor <= 0 disables the cap: balance may go arbitrarily negative.
	newBal, charged, err := d.ChargeWithFloor(ctx, uid, TxKindCharge, 100, "huge", "", 0)
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	if !approxEq(charged, 100) || !approxEq(newBal, -100) {
		t.Fatalf("floor should be disabled: charged=%v bal=%v", charged, newBal)
	}
}
