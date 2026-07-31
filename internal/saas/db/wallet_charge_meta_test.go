package db

import (
	"context"
	"testing"
	"time"
)

// A charge must land its structured attribution AND keep writing the legacy ref.
// The ref is what /billing/transactions and /workspaces/:id/ledger render today,
// so dropping it would silently blank out two live views.
func TestChargeRecordsMetaAndKeepsLegacyRef(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "meta@example.com")
	wsID, err := d.PersonalWorkspaceID(ctx, uid)
	if err != nil {
		t.Fatalf("personal ws: %v", err)
	}
	if _, err := d.AddBalance(ctx, uid, TxKindAdjust, 100, "seed", "", true); err != nil {
		t.Fatalf("seed: %v", err)
	}

	meta := ChargeMeta{
		TokenID:           42,
		Model:             "claude-opus-4-8",
		InputTokens:       1200,
		OutputTokens:      340,
		CacheReadTokens:   9000,
		CacheCreateTokens: 77,
	}
	ref := "token=42 model=claude-opus-4-8"
	if _, _, err := d.ChargeWorkspaceWithFloor(ctx, wsID, uid, TxKindCharge, 1.5, ref, "", 0, meta); err != nil {
		t.Fatalf("charge: %v", err)
	}

	var got ChargeMeta
	var gotRef string
	var amount float64
	if err := d.QueryRowContext(ctx, `
		SELECT token_id, model, input_tokens, output_tokens, cache_read_tokens, cache_create_tokens, ref, amount_usd
		FROM wallet_tx WHERE kind = 'charge' ORDER BY id DESC LIMIT 1`).
		Scan(&got.TokenID, &got.Model, &got.InputTokens, &got.OutputTokens,
			&got.CacheReadTokens, &got.CacheCreateTokens, &gotRef, &amount); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != meta {
		t.Errorf("meta round-trip: got %+v, want %+v", got, meta)
	}
	if gotRef != ref {
		t.Errorf("legacy ref not preserved: got %q, want %q", gotRef, ref)
	}
	if !approxEq(amount, -1.5) {
		t.Errorf("amount = %v, want -1.5", amount)
	}
}

// Money-in (topup / adjust / bonus / gift) has no key or model. Those rows must
// land on the column defaults, not on some phantom key — otherwise a top-up would
// show up as spend attributed to token 0 in every per-key report.
func TestTopupLeavesAttributionEmpty(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "topup@example.com")

	if _, err := d.AddBalance(ctx, uid, TxKindTopup, 25, "2026071312345", "", false); err != nil {
		t.Fatalf("topup: %v", err)
	}
	var tokenID int64
	var model string
	var inTok int64
	if err := d.QueryRowContext(ctx, `SELECT token_id, model, input_tokens FROM wallet_tx WHERE kind = 'topup'`).
		Scan(&tokenID, &model, &inTok); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if tokenID != 0 || model != "" || inTok != 0 {
		t.Fatalf("topup row carries attribution: token=%d model=%q input=%d", tokenID, model, inTok)
	}
}

// Per-key caps must only see charges attributed to that key. A user's sibling
// keys and unattributed legacy rows must not consume its allowance.
func TestSumChargeSinceForTokenIsScopedToToken(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "token-scope@example.com")
	wsID, err := d.PersonalWorkspaceID(ctx, uid)
	if err != nil {
		t.Fatalf("personal ws: %v", err)
	}

	first, err := d.CreateUserToken(ctx, uid, TokenParams{Name: "first"})
	if err != nil {
		t.Fatalf("create first token: %v", err)
	}
	second, err := d.CreateUserToken(ctx, uid, TokenParams{Name: "second"})
	if err != nil {
		t.Fatalf("create second token: %v", err)
	}

	charge := func(amount float64, tokenID int64) {
		t.Helper()
		if _, _, err := d.ChargeWorkspaceWithFloor(
			ctx, wsID, uid, TxKindCharge, amount, "token-scope", "", 0,
			ChargeMeta{TokenID: tokenID},
		); err != nil {
			t.Fatalf("charge token %d: %v", tokenID, err)
		}
	}
	charge(3, first.ID)
	charge(7, second.ID)
	charge(11, 0) // deliberately unattributed

	since := time.Unix(0, 0)
	if got, err := d.SumChargeSinceForToken(ctx, uid, first.ID, since); err != nil || !approxEq(got, 3) {
		t.Fatalf("first token spend = %v, err=%v; want 3", got, err)
	}
	if got, err := d.SumChargeSinceForToken(ctx, uid, second.ID, since); err != nil || !approxEq(got, 7) {
		t.Fatalf("second token spend = %v, err=%v; want 7", got, err)
	}
	if got, err := d.SumChargeSince(ctx, uid, since); err != nil || !approxEq(got, 21) {
		t.Fatalf("user spend = %v, err=%v; want 21", got, err)
	}
	if got, err := d.SumChargeSinceForWorkspace(ctx, wsID, since); err != nil || !approxEq(got, 21) {
		t.Fatalf("workspace spend = %v, err=%v; want 21", got, err)
	}
	if got, err := d.SumChargeSinceForMember(ctx, wsID, uid, since); err != nil || !approxEq(got, 21) {
		t.Fatalf("member spend = %v, err=%v; want 21", got, err)
	}
}

// A charge clamped to zero by the overdraft floor writes NO row. The reports
// count charge rows as billable events, so a phantom zero row would invent an
// event that never cost anyone anything.
func TestFloorClampedChargeWritesNoRow(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "clamped@example.com")
	wsID, err := d.PersonalWorkspaceID(ctx, uid)
	if err != nil {
		t.Fatalf("personal ws: %v", err)
	}

	// Drive the wallet to exactly the floor, then try to charge again.
	if _, _, err := d.ChargeWorkspaceWithFloor(ctx, wsID, uid, TxKindCharge, 50, "token=1 model=m", "", 10, ChargeMeta{TokenID: 1}); err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if _, charged, err := d.ChargeWorkspaceWithFloor(ctx, wsID, uid, TxKindCharge, 5, "token=1 model=m", "", 10, ChargeMeta{TokenID: 1}); err != nil {
		t.Fatalf("second charge: %v", err)
	} else if charged != 0 {
		t.Fatalf("expected the floor to clamp to zero, charged %v", charged)
	}

	var n int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM wallet_tx WHERE kind = 'charge'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("got %d charge rows, want 1 — the clamped-to-zero charge must not write one", n)
	}
}
