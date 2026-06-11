package growth_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/growth"
)

// openTestDB spins up a fresh SaaS SQLite DB (migrations v1..vN, including the
// growth tables) in a temp dir and returns it plus a growth service wired to
// the real wallet ledger.
func openTestDB(t *testing.T) (*db.DB, *growth.Service) {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, growth.New(store.DB, store)
}

func mkUser(t *testing.T, store *db.DB, email string) int64 {
	t.Helper()
	ctx := context.Background()
	g, err := store.DefaultGroup(ctx)
	if err != nil {
		t.Fatalf("default group: %v", err)
	}
	u, err := store.CreateUser(ctx, email, "x", db.RoleUser, g.ID, true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}

func TestChannelCRUD(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestDB(t)

	ch, err := svc.CreateChannel(ctx, growth.ChannelParams{Slug: "x", Name: "Twitter", BonusUSD: 2, Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ch.Slug != "x" || ch.BonusUSD != 2 || !ch.Enabled {
		t.Fatalf("unexpected channel: %+v", ch)
	}

	// Duplicate slug must fail (UNIQUE).
	if _, err := svc.CreateChannel(ctx, growth.ChannelParams{Slug: "x"}); err == nil {
		t.Fatal("expected duplicate-slug error, got nil")
	}

	got, err := svc.GetChannelBySlug(ctx, "x")
	if err != nil || got.ID != ch.ID {
		t.Fatalf("get by slug: %v / %+v", err, got)
	}

	if _, err := svc.UpdateChannel(ctx, ch.ID, growth.ChannelParams{Name: "X", BonusUSD: 5, Enabled: false}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = svc.GetChannel(ctx, ch.ID)
	if got.Name != "X" || got.BonusUSD != 5 || got.Enabled {
		t.Fatalf("update not applied: %+v", got)
	}

	if err := svc.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetChannel(ctx, ch.ID); err == nil {
		t.Fatal("expected ErrNotFound after delete")
	}
}

func TestVisitFirstTouchIdempotent(t *testing.T) {
	ctx := context.Background()
	_, svc := openTestDB(t)
	if _, err := svc.CreateChannel(ctx, growth.ChannelParams{Slug: "ins", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	// Two visits from the same visitor collapse to one first-touch row.
	for range 3 {
		if err := svc.RecordVisit(ctx, "ins", "vid-1"); err != nil {
			t.Fatalf("record visit: %v", err)
		}
	}
	if err := svc.RecordVisit(ctx, "ins", "vid-2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AccumulateDwell(ctx, "ins", "vid-1", 30_000); err != nil {
		t.Fatal(err)
	}

	stats, err := svc.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("want 1 channel, got %d", len(stats))
	}
	if stats[0].Visitors != 2 {
		t.Fatalf("want 2 unique visitors, got %d", stats[0].Visitors)
	}
	if stats[0].AvgDwellMS != 30_000 {
		t.Fatalf("want avg dwell 30000, got %d", stats[0].AvgDwellMS)
	}
}

func TestGrantSignupBonusAndROI(t *testing.T) {
	ctx := context.Background()
	store, svc := openTestDB(t)
	if _, err := svc.CreateChannel(ctx, growth.ChannelParams{Slug: "x", Name: "X", BonusUSD: 3, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// A visit precedes the signup so conversion can be linked by visitor id.
	if err := svc.RecordVisit(ctx, "x", "vid-9"); err != nil {
		t.Fatal(err)
	}
	uid := mkUser(t, store, "a@b.com")

	bonus, channel, err := svc.GrantSignupBonus(ctx, uid, "x", "vid-9")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if bonus != 3 || channel != "X" {
		t.Fatalf("want bonus 3 / channel X, got %v / %q", bonus, channel)
	}

	// Wallet credited through the audited ledger.
	bal, err := store.GetBalance(ctx, uid)
	if err != nil || bal != 3 {
		t.Fatalf("balance: %v / %v", bal, err)
	}

	// Simulate downstream money movement: a $10 topup and a $4 charge.
	if _, err := store.AddBalance(ctx, uid, db.TxKindTopup, 10, "t", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddBalance(ctx, uid, db.TxKindCharge, -4, "c", "", true); err != nil {
		t.Fatal(err)
	}

	stats, err := svc.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("want 1 channel, got %d", len(stats))
	}
	s := stats[0]
	if s.Signups != 1 {
		t.Fatalf("want 1 signup, got %d", s.Signups)
	}
	if s.Visitors != 1 || s.ConversionR != 1 {
		t.Fatalf("want conversion 1.0 (1/1), got %v (%d/%d)", s.ConversionR, s.Signups, s.Visitors)
	}
	if s.BonusPaid != 3 {
		t.Fatalf("want bonus_paid 3, got %v", s.BonusPaid)
	}
	if s.ToppedUpUSD != 10 {
		t.Fatalf("want topped_up 10, got %v", s.ToppedUpUSD)
	}
	if s.SpentUSD != 4 {
		t.Fatalf("want spent 4, got %v", s.SpentUSD)
	}

	// Second grant for the same user is a no-op: one channel credited per user,
	// and crucially the bonus is NOT paid out again.
	bonus2, _, err := svc.GrantSignupBonus(ctx, uid, "x", "vid-9")
	if err != nil {
		t.Fatalf("second grant: %v", err)
	}
	if bonus2 != 0 {
		t.Fatalf("want second grant to pay 0, got %v", bonus2)
	}
	if bal, _ := store.GetBalance(ctx, uid); bal != 9 { // 3 bonus + 10 topup - 4 charge
		t.Fatalf("want balance unchanged at 9 after re-grant, got %v", bal)
	}
	stats2, _ := svc.Stats(ctx)
	if stats2[0].Signups != 1 {
		t.Fatalf("want signups still 1 after re-grant, got %d", stats2[0].Signups)
	}
}

func TestGrantUnknownOrDisabledChannel(t *testing.T) {
	ctx := context.Background()
	store, svc := openTestDB(t)
	uid := mkUser(t, store, "u@b.com")

	// Unknown ref: no error, no bonus.
	if bonus, _, err := svc.GrantSignupBonus(ctx, uid, "nope", ""); err != nil || bonus != 0 {
		t.Fatalf("unknown ref: bonus=%v err=%v", bonus, err)
	}
	// Disabled channel: conversion recorded but no bonus.
	if _, err := svc.CreateChannel(ctx, growth.ChannelParams{Slug: "off", BonusUSD: 5, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if bonus, _, err := svc.GrantSignupBonus(ctx, uid, "off", ""); err != nil || bonus != 0 {
		t.Fatalf("disabled channel: bonus=%v err=%v", bonus, err)
	}
	if bal, _ := store.GetBalance(ctx, uid); bal != 0 {
		t.Fatalf("want balance 0 for disabled channel, got %v", bal)
	}
}
