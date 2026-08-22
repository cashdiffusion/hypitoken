package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The 2026-08-22 incident in miniature. Everything here is a property the
// production failure actually had: the file on disk stayed valid, the process
// lost its handles to it, and the fix was to reconnect rather than to repair.

func openTestDB(t *testing.T) *DB {
	t.Helper()
	p := filepath.Join(t.TempDir(), "saas.db")
	d, err := Open(p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// errCorrupt is what the driver hands back during the incident. Report matches
// on message text (see IsCorruptionError), so a plain error is a faithful
// stand-in and the test does not depend on driver internals.
var errCorrupt = errors.New("disk I/O error (522)")

// TestSelfHealRecoversFromUnlinkedWALAndSHM is the incident itself: something
// outside the process removes the live -wal and -shm while the server holds
// them open. The file the path names is still a perfectly good database, so
// recycling the pool has to restore service — and must not lose the row that
// was committed before the unlink.
func TestSelfHealRecoversFromUnlinkedWALAndSHM(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	if _, err := d.ExecContext(ctx, `CREATE TABLE probe (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO probe (v) VALUES ('before-the-unlink')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Fold the WAL into the main file first. The production database had also
	// checkpointed (its -wal was 0 bytes on disk), which is why nothing was
	// lost there either; a test that skipped this would be asserting that
	// unlinking a WAL with live content is safe, which it is not.
	if _, err := d.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	// The unlink. The server's open connections keep pointing at the now
	// unreferenced inodes.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(d.Path() + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove %s: %v", suffix, err)
		}
	}

	// A corruption error surfaces from the billing path.
	d.Report(errCorrupt)
	if !d.Corrupted() {
		t.Fatal("Report did not latch the corruption flag")
	}

	// Report schedules the heal asynchronously; run it synchronously here so
	// the test does not race the goroutine.
	d.recoverNow(IntegrityAlert{Source: "live traffic", Detail: errCorrupt.Error(), At: time.Now()})

	if d.Corrupted() {
		t.Fatal("self-heal left the database marked corrupt even though the file was intact")
	}
	if got := d.Heals(); got != 1 {
		t.Fatalf("Heals() = %d, want 1", got)
	}

	// The point of all of it: the database works and the data is there.
	var v string
	if err := d.QueryRowContext(ctx, `SELECT v FROM probe WHERE id = 1`).Scan(&v); err != nil {
		t.Fatalf("read after heal: %v", err)
	}
	if v != "before-the-unlink" {
		t.Fatalf("row = %q, want %q", v, "before-the-unlink")
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO probe (v) VALUES ('after-the-heal')`); err != nil {
		t.Fatalf("write after heal: %v", err)
	}
}

// TestSelfHealKeepsTrippedWhenFileIsGenuinelyDamaged is the other half of the
// diagnosis, and the reason the heal re-runs quick_check instead of just
// declaring victory. Recycling connections cannot fix damaged bytes, so the
// 2026-08-18 shape of this failure must still reach a human.
func TestSelfHealKeepsTrippedWhenFileIsGenuinelyDamaged(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "saas.db")

	seed, err := Open(p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	if _, err := seed.ExecContext(ctx, `CREATE TABLE probe (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for range 200 {
		if _, err := seed.ExecContext(ctx, `INSERT INTO probe (v) VALUES (?)`, "row"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if _, err := seed.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Scribble over the b-tree pages after the header. quick_check reads the
	// page structure, so this is corruption it can actually see.
	f, err := os.OpenFile(p, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("reopen for damage: %v", err)
	}
	junk := make([]byte, 8192)
	for i := range junk {
		junk[i] = 0xA5
	}
	if _, err := f.WriteAt(junk, 4096); err != nil {
		t.Fatalf("scribble: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close damaged: %v", err)
	}

	// Attach to the damaged file directly. Open() would run migrations and
	// fail; the incident's shape is a server that is ALREADY up when the file
	// goes bad, so bypassing migrate is the faithful reconstruction.
	sdb, err := sql.Open("sqlite", "file:"+p+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	sdb.SetMaxOpenConns(maxOpenConns)
	sdb.SetMaxIdleConns(maxIdleConns)
	d := &DB{DB: sdb, path: p}
	t.Cleanup(func() { _ = sdb.Close() })

	if err := d.QuickCheck(ctx); err == nil {
		t.Skip("quick_check accepted the scribbled file; nothing to assert about a damaged one")
	}

	d.Report(errCorrupt)
	d.recoverNow(IntegrityAlert{Source: "live traffic", Detail: errCorrupt.Error(), At: time.Now()})

	if !d.Corrupted() {
		t.Fatal("self-heal cleared the corruption flag on a genuinely damaged file")
	}
	if got := d.Heals(); got != 0 {
		t.Fatalf("Heals() = %d on a damaged file, want 0", got)
	}
}

// TestRecycleConnsRestoresPoolLimits guards the invariant that a heal is
// invisible afterwards. Leaving MaxIdleConns at 0 would turn every subsequent
// query into a fresh open() — the database would work and quietly get slower,
// which is the kind of regression that survives for months.
func TestRecycleConnsRestoresPoolLimits(t *testing.T) {
	d := openTestDB(t)

	d.recycleConns()

	stats := d.Stats()
	if stats.MaxOpenConnections != maxOpenConns {
		t.Fatalf("MaxOpenConnections = %d after recycle, want %d", stats.MaxOpenConnections, maxOpenConns)
	}
	// Exercise the pool: idle connections should be retained again.
	ctx := context.Background()
	for i := range 3 {
		if err := d.PingContext(ctx); err != nil {
			t.Fatalf("ping %d: %v", i, err)
		}
	}
	if idle := d.Stats().Idle; idle == 0 {
		t.Fatal("no idle connections retained after recycle — MaxIdleConns was not restored")
	}
}

// TestReportDoesNotBlockTheCallingGoroutine matters because the only
// live-traffic caller is a defer on the billing hot path. The heal sleeps
// through connRecycleGrace and then runs quick_check; doing that inline would
// stall every charge behind it.
func TestReportDoesNotBlockTheCallingGoroutine(t *testing.T) {
	d := openTestDB(t)

	start := time.Now()
	d.Report(errCorrupt)
	if elapsed := time.Since(start); elapsed > connRecycleGrace {
		t.Fatalf("Report blocked for %s — it must hand off to a goroutine", elapsed)
	}
}

// TestReportIgnoresOrdinaryErrors keeps the heal from firing on the constraint
// and busy errors that ordinary traffic produces all day.
func TestReportIgnoresOrdinaryErrors(t *testing.T) {
	d := openTestDB(t)

	d.Report(errors.New("UNIQUE constraint failed: users.email"))
	d.Report(sql.ErrNoRows)
	d.Report(nil)

	if d.Corrupted() {
		t.Fatal("an ordinary error latched the corruption flag")
	}
	if got := d.Heals(); got != 0 {
		t.Fatalf("Heals() = %d, want 0", got)
	}
}

// TestSelfHealIsThrottled: a broken database emits errors continuously, and
// every one of them routes to the heal. Without the cooldown the pool would be
// recycled thousands of times a minute, each one a 250ms stall of new
// connections.
func TestSelfHealIsThrottled(t *testing.T) {
	d := openTestDB(t)
	a := IntegrityAlert{Source: "live traffic", Detail: errCorrupt.Error(), At: time.Now()}

	d.recoverNow(a) // lastAttempt is 0, so this one runs
	if got := d.Heals(); got != 1 {
		t.Fatalf("Heals() = %d after first attempt, want 1", got)
	}

	// Immediately again: inside recoverCooldown, so it must be skipped.
	d.recoverNow(a)
	if got := d.Heals(); got != 1 {
		t.Fatalf("Heals() = %d after a throttled attempt, want 1", got)
	}

	// Wind the clock back past the cooldown and it should run again.
	d.recov.lastAttempt.Store(time.Now().Add(-2 * recoverCooldown).Unix())
	d.recoverNow(a)
	if got := d.Heals(); got != 2 {
		t.Fatalf("Heals() = %d after the cooldown expired, want 2", got)
	}
}

// TestSelfHealGivesUpAfterMaxAttempts stops an unfixable file from being
// retried forever, and TestSelfHealAttemptBudgetResets stops that giving-up
// from being permanent.
func TestSelfHealGivesUpAfterMaxAttempts(t *testing.T) {
	d := openTestDB(t)
	a := IntegrityAlert{Source: "scheduled check", Detail: errCorrupt.Error(), At: time.Now()}

	// Burn the budget without letting a success reset it: a healthy file would
	// zero `attempts` on every pass, so drive the counter directly.
	d.recov.attempts.Store(maxRecoverAttempts)
	d.recov.lastAttempt.Store(time.Now().Add(-2 * recoverCooldown).Unix())

	d.recoverNow(a)
	if got := d.Heals(); got != 0 {
		t.Fatalf("Heals() = %d, want 0 — the attempt budget was exhausted and the heal should have been skipped", got)
	}
}

func TestSelfHealAttemptBudgetResets(t *testing.T) {
	d := openTestDB(t)
	a := IntegrityAlert{Source: "scheduled check", Detail: errCorrupt.Error(), At: time.Now()}

	d.recov.attempts.Store(maxRecoverAttempts)
	// Quiet for longer than recoverAttemptReset: a new incident, not the old one.
	d.recov.lastAttempt.Store(time.Now().Add(-2 * recoverAttemptReset).Unix())

	d.recoverNow(a)
	if got := d.Heals(); got != 1 {
		t.Fatalf("Heals() = %d, want 1 — a fresh incident should get its attempts back", got)
	}
}

// TestSelfHealNotifiesWithResolved checks the operator gets an all-clear that
// is distinguishable from the page they already received.
func TestSelfHealNotifiesWithResolved(t *testing.T) {
	d := openTestDB(t)

	got := make(chan IntegrityAlert, 4)
	h := func(a IntegrityAlert) { got <- a }
	d.corrupt.handler.Store(&h)

	d.recoverNow(IntegrityAlert{Source: "live traffic", Detail: errCorrupt.Error(), At: time.Now()})

	select {
	case a := <-got:
		if !a.Resolved {
			t.Fatalf("handler got Resolved=false for a successful heal (source %q)", a.Source)
		}
		if a.Source != "self-heal" {
			t.Fatalf("Source = %q, want %q", a.Source, "self-heal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no all-clear delivered after a successful heal")
	}
}

// TestOpenReadOnlyRefusesWrites: the guarantee OpenReadOnly makes is the whole
// reason reporting tools may point at a live production database, so it is
// worth asserting rather than trusting to a DSN string staying correct.
func TestOpenReadOnlyRefusesWrites(t *testing.T) {
	p := filepath.Join(t.TempDir(), "saas.db")
	seed, err := Open(p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ro, err := OpenReadOnly(p)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer func() { _ = ro.Close() }()

	ctx := context.Background()
	// Reading is the point, so it must work.
	var n int
	if err := ro.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("read through a read-only handle: %v", err)
	}
	// Writing must not.
	if _, err := ro.ExecContext(ctx, `INSERT INTO groups (name) VALUES ('should-not-land')`); err == nil {
		t.Fatal("a write succeeded through OpenReadOnly")
	}
}

// TestOpenReadOnlyRejectsAMissingFile stops a typo'd path from being reported
// as an empty database, which for a reconciliation tool would read as
// "nothing to repair".
func TestOpenReadOnlyRejectsAMissingFile(t *testing.T) {
	if _, err := OpenReadOnly(filepath.Join(t.TempDir(), "absent.db")); err == nil {
		t.Fatal("OpenReadOnly invented a database that does not exist")
	}
}
