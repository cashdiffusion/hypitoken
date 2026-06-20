package db

import (
	"context"
	"path/filepath"
	"testing"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func mkUser(t *testing.T, d *DB, email string) int64 {
	t.Helper()
	u, err := d.CreateUser(context.Background(), email, "hash", "user", 1, true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}

func TestDefaultNicknameStable(t *testing.T) {
	if a, b := defaultNicknameFor(42), defaultNicknameFor(42); a != b {
		t.Fatalf("default nickname not stable: %q vs %q", a, b)
	}
	if defaultNicknameFor(1) == defaultNicknameFor(2) {
		t.Fatal("distinct ids produced identical nicknames")
	}
	got := defaultNicknameFor(3)
	if len(got) < 6 || got[:5] != "hypi-" {
		t.Fatalf("unexpected nickname form: %q", got)
	}
}

func TestGetOrCreateAndUpdateProfile(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "a@example.com")

	p, err := d.GetOrCreateProfile(ctx, uid)
	if err != nil {
		t.Fatalf("get/create: %v", err)
	}
	if !p.NameIsDefault {
		t.Fatal("fresh profile should have name_is_default=true")
	}
	if p.PublicOptIn {
		t.Fatal("fresh profile should default to private (opt-out)")
	}

	// Rename → name_is_default flips to false; opt in.
	name := "Cool Dev"
	yes := true
	p2, err := d.UpdateProfile(ctx, uid, &name, &yes)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if p2.DisplayName != "Cool Dev" || p2.NameIsDefault || !p2.PublicOptIn {
		t.Fatalf("update not applied: %+v", p2)
	}
}

func TestBumpActivityAndLeaderboard(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	u1 := mkUser(t, d, "u1@example.com")
	u2 := mkUser(t, d, "u2@example.com")
	u3 := mkUser(t, d, "u3@example.com") // never active → must not appear

	// u1: 300 tokens over 2 requests; u2: 100 tokens over 1 request.
	d.BumpActivity(ctx, u1, 200)
	d.BumpActivity(ctx, u1, 100)
	d.BumpActivity(ctx, u2, 100)
	_ = u3

	rows, err := d.Leaderboard(ctx, MetricTokens, 100)
	if err != nil {
		t.Fatalf("leaderboard: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 active rows, got %d", len(rows))
	}
	if rows[0].UserID != u1 || rows[0].LifetimeTokens != 300 || rows[0].LifetimeRequests != 2 {
		t.Fatalf("row0 wrong: %+v", rows[0])
	}
	if rows[1].UserID != u2 {
		t.Fatalf("ordering wrong: %+v", rows)
	}

	// Rank checks.
	if r, _ := d.RankOf(ctx, u1, MetricTokens); r != 1 {
		t.Fatalf("u1 rank: want 1, got %d", r)
	}
	if r, _ := d.RankOf(ctx, u2, MetricTokens); r != 2 {
		t.Fatalf("u2 rank: want 2, got %d", r)
	}
	if r, _ := d.RankOf(ctx, u3, MetricTokens); r != 0 {
		t.Fatalf("inactive user should rank 0, got %d", r)
	}

	// By requests, u1 (2) still leads u2 (1).
	rreq, _ := d.Leaderboard(ctx, MetricRequests, 100)
	if rreq[0].UserID != u1 {
		t.Fatalf("requests leaderboard wrong: %+v", rreq)
	}
}
