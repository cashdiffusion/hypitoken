package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// Reconciliation moves real money, so these tests assert the arithmetic and
// the replay behaviour rather than just that the command runs.

const (
	testCodexMultiplier  = 0.05
	testClaudeMultiplier = 0.3
)

// newRequestIndex builds a stand-in for <log_dir>/requests.db carrying only the
// columns reconciliation reads. The real schema is cc-core's and much wider;
// duplicating all of it here would test cc-core, not this command.
func newRequestIndex(t *testing.T, dir string) *sql.DB {
	t.Helper()
	rdb, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "requests.db"))
	if err != nil {
		t.Fatalf("open request index: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	if _, err := rdb.Exec(`CREATE TABLE req (
		id              INTEGER PRIMARY KEY,
		ts              INTEGER NOT NULL,
		client_token    TEXT    NOT NULL DEFAULT '',
		provider        TEXT    NOT NULL DEFAULT '',
		input           INTEGER NOT NULL DEFAULT 0,
		output          INTEGER NOT NULL DEFAULT 0,
		cache_read      INTEGER NOT NULL DEFAULT 0,
		cache_create    INTEGER NOT NULL DEFAULT 0,
		cache_create_1h INTEGER NOT NULL DEFAULT 0,
		cost_usd        REAL    NOT NULL DEFAULT 0,
		billed_usd      REAL    NOT NULL DEFAULT 0,
		multiplier      REAL    NOT NULL DEFAULT 0,
		attempt_only    INTEGER NOT NULL DEFAULT 0,
		user_id         INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatalf("create req: %v", err)
	}
	return rdb
}

// insertReq writes one request-log row. userID 0 with multiplier 0 is exactly
// what proxy.go leaves behind when Charge failed — the marker reconciliation
// keys on.
func insertReq(t *testing.T, rdb *sql.DB, ts time.Time, token, provider string, cost float64, userID int64, attemptOnly int) {
	t.Helper()
	mult := 0.0
	if userID != 0 {
		mult = testCodexMultiplier
	}
	if _, err := rdb.Exec(
		`INSERT INTO req (ts, client_token, provider, input, output, cost_usd, billed_usd, multiplier, attempt_only, user_id)
		 VALUES (?, ?, ?, 100, 20, ?, ?, ?, ?, ?)`,
		ts.UnixNano(), token, provider, cost, cost, mult, attemptOnly, userID); err != nil {
		t.Fatalf("insert req: %v", err)
	}
}

// seedSaaS creates a funded user with one token and returns the token secret,
// its workspace, and the starting balance.
func seedSaaS(t *testing.T, path string, startUSD float64) (secret string, wsID int64) {
	t.Helper()
	d, err := saasdb.Open(path)
	if err != nil {
		t.Fatalf("open saas db: %v", err)
	}
	defer func() { _ = d.Close() }()

	ctx := context.Background()
	u, err := d.CreateUser(ctx, "recon@example.test", "hash", "user", 1, true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	tok, err := d.CreateUserToken(ctx, u.ID, saasdb.TokenParams{Name: "recon"})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if _, err := d.AddBalance(ctx, u.ID, "topup", startUSD, "seed", "seed", false); err != nil {
		t.Fatalf("fund: %v", err)
	}
	ws, err := d.PersonalWorkspaceID(ctx, u.ID)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	return tok.Token, ws
}

func balanceOf(t *testing.T, path string, wsID int64) float64 {
	t.Helper()
	d, err := saasdb.Open(path)
	if err != nil {
		t.Fatalf("reopen saas db: %v", err)
	}
	defer func() { _ = d.Close() }()
	bal, err := d.GetWorkspaceBalance(context.Background(), wsID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	return bal
}

// TestReconcileChargesSettlesDroppedRowsOnce is the whole feature: the dropped
// charges are billed at the live multiplier, and re-running the same window
// does not bill them a second time.
func TestReconcileChargesSettlesDroppedRowsOnce(t *testing.T) {
	dir := t.TempDir()
	saasPath := filepath.Join(dir, "saas.db")
	secret, wsID := seedSaaS(t, saasPath, 100)

	rdb := newRequestIndex(t, dir)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	from := base.Add(-time.Minute)
	to := base.Add(10 * time.Minute)

	// Three dropped codex requests: $10 of official cost inside the window.
	insertReq(t, rdb, base, secret, "openai", 4, 0, 0)
	insertReq(t, rdb, base.Add(time.Minute), secret, "openai", 3.5, 0, 0)
	insertReq(t, rdb, base.Add(2*time.Minute), secret, "openai", 2.5, 0, 0)

	wantOwed := 10 * testCodexMultiplier

	// A dry run must not move money.
	if err := reconcileCharges(dir, saasPath, 0, from, to, false); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if got := balanceOf(t, saasPath, wsID); got != 100 {
		t.Fatalf("balance = %v after a dry run, want it untouched at 100", got)
	}

	if err := reconcileCharges(dir, saasPath, 0, from, to, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := balanceOf(t, saasPath, wsID)
	if diff := (100 - wantOwed) - got; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("balance = %v after settling, want %v (owed %v)", got, 100-wantOwed, wantOwed)
	}

	// The replay: same window again, no second debit. This is what makes the
	// command safe to re-run when nobody remembers whether it was already run.
	if err := reconcileCharges(dir, saasPath, 0, from, to, true); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if again := balanceOf(t, saasPath, wsID); again != got {
		t.Fatalf("balance moved on re-run: %v → %v; the idempotency key is not holding", got, again)
	}
}

// TestReconcileChargesIgnoresSuccessfullyBilledAndAttemptRows guards the
// selection itself. Billing a row that was already billed is the one outcome
// worse than not billing it at all.
func TestReconcileChargesIgnoresSuccessfullyBilledAndAttemptRows(t *testing.T) {
	dir := t.TempDir()
	saasPath := filepath.Join(dir, "saas.db")
	secret, wsID := seedSaaS(t, saasPath, 50)

	rdb := newRequestIndex(t, dir)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	from := base.Add(-time.Minute)
	to := base.Add(10 * time.Minute)

	// Billed fine (user_id set) — must be left alone.
	insertReq(t, rdb, base, secret, "openai", 8, 42, 0)
	// A failover attempt, not a served request — never billable.
	insertReq(t, rdb, base.Add(time.Minute), secret, "openai", 9, 0, 1)
	// Zero-cost row — nothing to collect.
	insertReq(t, rdb, base.Add(2*time.Minute), secret, "openai", 0, 0, 0)

	if err := reconcileCharges(dir, saasPath, 0, from, to, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := balanceOf(t, saasPath, wsID); got != 50 {
		t.Fatalf("balance = %v, want 50 — reconciliation billed rows it should have ignored", got)
	}
}

// TestReconcileChargesRespectsTheWindow: a window is an assertion about which
// outage is being repaired, and traffic on either side of it was billed
// normally.
func TestReconcileChargesRespectsTheWindow(t *testing.T) {
	dir := t.TempDir()
	saasPath := filepath.Join(dir, "saas.db")
	secret, wsID := seedSaaS(t, saasPath, 50)

	rdb := newRequestIndex(t, dir)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	from := base
	to := base.Add(5 * time.Minute)

	insertReq(t, rdb, base.Add(-time.Minute), secret, "openai", 6, 0, 0)  // before
	insertReq(t, rdb, base.Add(time.Minute), secret, "openai", 2, 0, 0)   // inside
	insertReq(t, rdb, base.Add(9*time.Minute), secret, "openai", 7, 0, 0) // after

	if err := reconcileCharges(dir, saasPath, 0, from, to, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := 50 - 2*testCodexMultiplier
	if got := balanceOf(t, saasPath, wsID); got != want {
		t.Fatalf("balance = %v, want %v — only the in-window row should have been settled", got, want)
	}
}

// TestReconcileChargesPricesPerProvider: claude and codex bill at different
// multipliers, and a token that used both during the outage owes the sum of two
// different rates, not one rate applied to the total.
func TestReconcileChargesPricesPerProvider(t *testing.T) {
	dir := t.TempDir()
	saasPath := filepath.Join(dir, "saas.db")
	secret, wsID := seedSaaS(t, saasPath, 100)

	rdb := newRequestIndex(t, dir)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	from := base.Add(-time.Minute)
	to := base.Add(10 * time.Minute)

	insertReq(t, rdb, base, secret, "openai", 10, 0, 0)
	insertReq(t, rdb, base.Add(time.Minute), secret, "anthropic", 10, 0, 0)

	want := 100 - (10*testCodexMultiplier + 10*testClaudeMultiplier)
	if err := reconcileCharges(dir, saasPath, 0, from, to, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := balanceOf(t, saasPath, wsID)
	if diff := want - got; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("balance = %v, want %v — providers must be priced at their own multipliers", got, want)
	}
}

// TestReconcileChargesSkipsUnresolvableTokens: a token deleted between the
// outage and the repair cannot be priced. Reporting it and moving on beats
// both guessing a workspace and aborting the whole run over one row.
func TestReconcileChargesSkipsUnresolvableTokens(t *testing.T) {
	dir := t.TempDir()
	saasPath := filepath.Join(dir, "saas.db")
	secret, wsID := seedSaaS(t, saasPath, 30)

	rdb := newRequestIndex(t, dir)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	from := base.Add(-time.Minute)
	to := base.Add(10 * time.Minute)

	insertReq(t, rdb, base, secret, "openai", 4, 0, 0)
	insertReq(t, rdb, base.Add(time.Minute), "sk-cpa-does-not-exist", "openai", 99, 0, 0)

	if err := reconcileCharges(dir, saasPath, 0, from, to, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := 30 - 4*testCodexMultiplier
	if got := balanceOf(t, saasPath, wsID); got != want {
		t.Fatalf("balance = %v, want %v — the unknown token must be skipped, not charged to someone", got, want)
	}
}

// TestReconcileChargesReportsNothingToDo keeps the no-op path from looking like
// a failure to an operator running it speculatively.
func TestReconcileChargesReportsNothingToDo(t *testing.T) {
	dir := t.TempDir()
	saasPath := filepath.Join(dir, "saas.db")
	_, _ = seedSaaS(t, saasPath, 10)
	newRequestIndex(t, dir)

	base := time.Now().Add(-time.Hour)
	if err := reconcileCharges(dir, saasPath, 0, base, base.Add(time.Minute), true); err != nil {
		t.Fatalf("empty window should not error: %v", err)
	}
}

// TestReadDroppedChargesLeavesTheIndexAlone is the operational guarantee that
// matters most here: the 2026-08-22 outage was caused by a second process
// disturbing a live SQLite file, and this command runs against exactly such a
// file. Reading must not create, delete, or write anything.
func TestReadDroppedChargesLeavesTheIndexAlone(t *testing.T) {
	dir := t.TempDir()
	rdb := newRequestIndex(t, dir)
	base := time.Now().Add(-time.Hour)
	insertReq(t, rdb, base, "sk-cpa-whatever", "openai", 1, 0, 0)

	path := filepath.Join(dir, "requests.db")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if _, err := readDroppedCharges(dir, base.Add(-time.Minute), base.Add(time.Minute)); err != nil {
		t.Fatalf("read: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("the request index changed during a read: size %d→%d, mtime %s→%s",
			before.Size(), after.Size(), before.ModTime(), after.ModTime())
	}
}

// TestReconcileChargesSurfacesAMissingIndex: pointing the command at the wrong
// directory should say so, not report a clean bill of health.
func TestReconcileChargesSurfacesAMissingIndex(t *testing.T) {
	dir := t.TempDir()
	saasPath := filepath.Join(dir, "saas.db")
	_, _ = seedSaaS(t, saasPath, 10)

	base := time.Now()
	err := reconcileCharges(filepath.Join(dir, "nope"), saasPath, 0, base.Add(-time.Hour), base, false)
	if err == nil {
		t.Fatal("a missing request index was reported as success")
	}
	if want := "request index"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not mention %q", err, want)
	}
}

// TestDryRunDoesNotWriteTheSaaSDatabase is an operational guarantee, not a
// nicety. A dry run is the thing an operator reaches for mid-incident, against
// a database the server is actively using — and a second read-write attachment
// to a live SQLite file is precisely what caused the 2026-08-22 outage. So the
// reporting path must leave the file untouched, byte for byte.
func TestDryRunDoesNotWriteTheSaaSDatabase(t *testing.T) {
	dir := t.TempDir()
	saasPath := filepath.Join(dir, "saas.db")
	secret, _ := seedSaaS(t, saasPath, 100)

	rdb := newRequestIndex(t, dir)
	base := time.Now().Add(-time.Hour)
	insertReq(t, rdb, base, secret, "openai", 5, 0, 0)

	before, err := os.Stat(saasPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if err := reconcileCharges(dir, saasPath, 0, base.Add(-time.Minute), base.Add(time.Minute), false); err != nil {
		t.Fatalf("dry run: %v", err)
	}

	after, err := os.Stat(saasPath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("dry run modified saas.db: size %d→%d, mtime %s→%s",
			before.Size(), after.Size(), before.ModTime(), after.ModTime())
	}
}
