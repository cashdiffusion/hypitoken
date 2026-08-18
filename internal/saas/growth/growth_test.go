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
		if err := svc.RecordVisit(ctx, "ins", "vid-1", ""); err != nil {
			t.Fatalf("record visit: %v", err)
		}
	}
	if err := svc.RecordVisit(ctx, "ins", "vid-2", ""); err != nil {
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
	if err := svc.RecordVisit(ctx, "x", "vid-9", ""); err != nil {
		t.Fatal(err)
	}
	uid := mkUser(t, store, "a@b.com")

	bonus, channel, matched, fraud, err := svc.GrantSignupBonus(ctx, uid, "x", "vid-9", "fp-roi", "", "a@b.com")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if bonus != 3 || channel != "X" || !matched || fraud {
		t.Fatalf("want bonus 3 / channel X / matched / !fraud, got %v / %q / %v / %v", bonus, channel, matched, fraud)
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
	bonus2, _, _, _, err := svc.GrantSignupBonus(ctx, uid, "x", "vid-9", "fp-roi", "", "a@b.com")
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

	// Unknown ref: no error, no bonus, no match (caller falls back to trial).
	if bonus, _, matched, _, err := svc.GrantSignupBonus(ctx, uid, "nope", "", "fp-n", "", "n@b.com"); err != nil || bonus != 0 || matched {
		t.Fatalf("unknown ref: bonus=%v matched=%v err=%v", bonus, matched, err)
	}
	// Disabled channel: conversion recorded but no bonus.
	if _, err := svc.CreateChannel(ctx, growth.ChannelParams{Slug: "off", BonusUSD: 5, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	// Disabled channel: matched (so caller skips trial) but no bonus paid.
	if bonus, _, matched, _, err := svc.GrantSignupBonus(ctx, uid, "off", "", "fp-o", "", "o@b.com"); err != nil || bonus != 0 || !matched {
		t.Fatalf("disabled channel: bonus=%v matched=%v err=%v", bonus, matched, err)
	}
	if bal, _ := store.GetBalance(ctx, uid); bal != 0 {
		t.Fatalf("want balance 0 for disabled channel, got %v", bal)
	}
}

// TestSignupFraudFingerprint verifies the anti-abuse path: a second signup from
// the same browser fingerprint is flagged and the channel bonus is withheld,
// while a different fingerprint on the same channel still pays out.
func TestSignupFraudFingerprint(t *testing.T) {
	ctx := context.Background()
	store, svc := openTestDB(t)
	if _, err := svc.CreateChannel(ctx, growth.ChannelParams{Slug: "x", Name: "X", BonusUSD: 3, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	// First user on fingerprint "fp-A": clean, bonus paid.
	u1 := mkUser(t, store, "a@b.com")
	bonus, _, matched, fraud, err := svc.GrantSignupBonus(ctx, u1, "x", "v1", "fp-A", "203.0.113.7", "a@b.com")
	if err != nil || bonus != 3 || !matched || fraud {
		t.Fatalf("first signup: bonus=%v matched=%v fraud=%v err=%v", bonus, matched, fraud, err)
	}
	if bal, _ := store.GetBalance(ctx, u1); bal != 3 {
		t.Fatalf("first user balance: want 3, got %v", bal)
	}

	// Second user, SAME fingerprint: flagged, no bonus, balance stays 0.
	u2 := mkUser(t, store, "b@b.com")
	bonus, _, matched, fraud, err = svc.GrantSignupBonus(ctx, u2, "x", "v2", "fp-A", "198.51.100.9", "b@b.com")
	if err != nil || !fraud || bonus != 0 || !matched {
		t.Fatalf("repeat fingerprint: bonus=%v matched=%v fraud=%v err=%v", bonus, matched, fraud, err)
	}
	if bal, _ := store.GetBalance(ctx, u2); bal != 0 {
		t.Fatalf("flagged user balance: want 0, got %v", bal)
	}

	// Third user, DIFFERENT fingerprint and subnet: clean again, bonus paid.
	u3 := mkUser(t, store, "c@b.com")
	bonus, _, _, fraud, err = svc.GrantSignupBonus(ctx, u3, "x", "v3", "fp-B", "192.0.2.5", "c@b.com")
	if err != nil || fraud || bonus != 3 {
		t.Fatalf("third signup: bonus=%v fraud=%v err=%v", bonus, fraud, err)
	}
}

// TestSignupFraudSubnet verifies the soft IP-subnet rule: once the configured
// number of distinct signups share a /24, the next one from that subnet is
// flagged even with a brand-new fingerprint.
func TestSignupFraudSubnet(t *testing.T) {
	ctx := context.Background()
	store, svc := openTestDB(t)
	svc.ConfigureFraud(growth.FraudConfig{Enabled: true, SubnetThreshold: 2})

	// Two clean signups from 203.0.113.0/24 (distinct fingerprints).
	for i, ip := range []string{"203.0.113.1", "203.0.113.2"} {
		uid := mkUser(t, store, string(rune('a'+i))+"@sub.com")
		_, _, _, fraud, err := svc.GrantSignupBonus(ctx, uid, "", "", "fp-"+ip, ip, "u@sub.com")
		if err != nil || fraud {
			t.Fatalf("subnet signup %d: fraud=%v err=%v", i, fraud, err)
		}
	}
	// Third from the same /24 trips the threshold (2 prior distinct users).
	uid := mkUser(t, store, "z@sub.com")
	_, _, _, fraud, err := svc.GrantSignupBonus(ctx, uid, "", "", "fp-new", "203.0.113.250", "z@sub.com")
	if err != nil || !fraud {
		t.Fatalf("subnet threshold: want fraud, got fraud=%v err=%v", fraud, err)
	}
}

// TestSignupFraudDisposableEmail covers the throwaway-mailbox rule. A wallet-
// bearing account on a single-use address is never a customer we want to fund.
func TestSignupFraudDisposableEmail(t *testing.T) {
	ctx := context.Background()
	store, svc := openTestDB(t)

	uid := mkUser(t, store, "burner@yopmail.com")
	_, _, _, fraud, err := svc.GrantSignupBonus(ctx, uid, "", "", "fp-1", "203.0.113.7", "burner@yopmail.com")
	if err != nil || !fraud {
		t.Fatalf("disposable domain: want fraud, got fraud=%v err=%v", fraud, err)
	}
	// Suffix rule: the 2026-08-08 farm rotated through sibling domains under one
	// free-subdomain parent, so the parent itself has to match.
	uid2 := mkUser(t, store, "farm@brandnew.dpdns.org")
	_, _, _, fraud, err = svc.GrantSignupBonus(ctx, uid2, "", "", "fp-2", "198.51.100.9", "farm@brandnew.dpdns.org")
	if err != nil || !fraud {
		t.Fatalf("disposable suffix: want fraud, got fraud=%v err=%v", fraud, err)
	}
	// Operator-supplied entries extend the built-ins.
	svc.ConfigureFraud(growth.FraudConfig{Enabled: true, DisposableDomains: []string{"blocked.example"}})
	uid3 := mkUser(t, store, "x@blocked.example")
	_, _, _, fraud, err = svc.GrantSignupBonus(ctx, uid3, "", "", "fp-3", "192.0.2.5", "x@blocked.example")
	if err != nil || !fraud {
		t.Fatalf("operator blocklist: want fraud, got fraud=%v err=%v", fraud, err)
	}
}

// TestSignupFraudNoFingerprint covers the scripted-signup rule: a registration
// that never ran the page's fingerprint script gets no bonus. 93 of the 168
// signups in the 2026-08-08 farm looked exactly like this.
func TestSignupFraudNoFingerprint(t *testing.T) {
	ctx := context.Background()
	store, svc := openTestDB(t)

	uid := mkUser(t, store, "script@gmail.com")
	_, _, _, fraud, err := svc.GrantSignupBonus(ctx, uid, "", "", "", "203.0.113.7", "script@gmail.com")
	if err != nil || !fraud {
		t.Fatalf("missing fingerprint: want fraud, got fraud=%v err=%v", fraud, err)
	}
	// Operators who register users server-side can turn the rule off.
	svc.ConfigureFraud(growth.FraudConfig{Enabled: true, RequireFingerprint: false})
	uid2 := mkUser(t, store, "legit@gmail.com")
	_, _, _, fraud, err = svc.GrantSignupBonus(ctx, uid2, "", "", "", "198.51.100.9", "legit@gmail.com")
	if err != nil || fraud {
		t.Fatalf("rule disabled: want clean, got fraud=%v err=%v", fraud, err)
	}
}

// TestSignupFraudEmailDomainBurst covers the domain-burst rule and its exemption
// for mainstream providers, where real users genuinely cluster.
func TestSignupFraudEmailDomainBurst(t *testing.T) {
	ctx := context.Background()
	store, svc := openTestDB(t)
	svc.ConfigureFraud(growth.FraudConfig{Enabled: true, RequireFingerprint: true, EmailDomainThreshold: 2})

	// Two signups on an attacker-controlled domain are still clean…
	for i, addr := range []string{"a@farm.test", "b@farm.test"} {
		uid := mkUser(t, store, addr)
		_, _, _, fraud, err := svc.GrantSignupBonus(ctx, uid, "", "", "fp-"+string(rune('a'+i)), "203.0.113."+string(rune('1'+i)), addr)
		if err != nil || fraud {
			t.Fatalf("domain signup %d: fraud=%v err=%v", i, fraud, err)
		}
	}
	// …the third trips the threshold.
	uid := mkUser(t, store, "c@farm.test")
	_, _, _, fraud, err := svc.GrantSignupBonus(ctx, uid, "", "", "fp-c", "192.0.2.5", "c@farm.test")
	if err != nil || !fraud {
		t.Fatalf("domain burst: want fraud, got fraud=%v err=%v", fraud, err)
	}
	// Mainstream providers are exempt however many sign up.
	for i, addr := range []string{"p@gmail.com", "q@gmail.com", "r@gmail.com"} {
		uid := mkUser(t, store, addr)
		_, _, _, fraud, err := svc.GrantSignupBonus(ctx, uid, "", "", "fpg-"+string(rune('a'+i)), "198.51.100."+string(rune('1'+i)), addr)
		if err != nil || fraud {
			t.Fatalf("gmail signup %d: want clean, got fraud=%v err=%v", i, fraud, err)
		}
	}
}

// A single row storing a REAL in the INTEGER duration_ms column used to take
// the entire admin attribution tab down with a 500 ("converting driver.Value
// type float64 to a int64"). SQLite is weakly typed, so the column
// declaration does not prevent it, and production carries exactly one such
// row (5.06). Stats must survive it.
func TestStatsToleratesRealDurationRow(t *testing.T) {
	store, svc := openTestDB(t)
	ctx := context.Background()

	if _, err := svc.CreateChannel(ctx, growth.ChannelParams{
		Slug: "dirty", Name: "Dirty", Enabled: true,
	}); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := svc.RecordVisit(ctx, "dirty", "visitor-1", ""); err != nil {
		t.Fatalf("record visit: %v", err)
	}
	// Write the float directly — the tracking API clamps to an int, so the
	// only way to reproduce production's row is to bypass it.
	if _, err := store.ExecContext(ctx,
		`UPDATE channel_visits SET duration_ms = 5.06 WHERE slug = 'dirty'`); err != nil {
		t.Fatalf("seed real duration: %v", err)
	}

	stats, err := svc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats with a REAL duration_ms row = %v, want nil", err)
	}
	if len(stats) == 0 {
		t.Fatal("Stats returned no channels")
	}
}
