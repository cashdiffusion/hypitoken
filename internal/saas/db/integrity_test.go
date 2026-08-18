package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestQuickCheckPassesOnHealthyDB(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close() //nolint:errcheck // test cleanup

	if err := d.QuickCheck(context.Background()); err != nil {
		t.Fatalf("QuickCheck on a fresh DB = %v, want nil", err)
	}
	if d.Corrupted() {
		t.Fatal("Corrupted() = true on a healthy DB")
	}
}

// The incident produced these three messages in rotation. SQLITE_IOERR_
// SHORT_READ ("disk I/O error") came first — the WAL index pointing past the
// end of the WAL file — and only once reads started landing on garbage pages
// did SQLITE_CORRUPT and SQLITE_NOTADB appear. All three have to escalate, and
// ordinary busy/constraint errors must not.
func TestIsCorruptionError(t *testing.T) {
	corrupt := []string{
		"database disk image is malformed (11)",
		"file is not a database (26)",
		"disk I/O error (522)",
		"wrapped: DATABASE DISK IMAGE IS MALFORMED",
	}
	for _, msg := range corrupt {
		if !IsCorruptionError(errors.New(msg)) {
			t.Errorf("IsCorruptionError(%q) = false, want true", msg)
		}
	}

	benign := []string{
		"database is locked (5)",
		"UNIQUE constraint failed: users.email",
		"context deadline exceeded",
		"no such table: widgets",
	}
	for _, msg := range benign {
		if IsCorruptionError(errors.New(msg)) {
			t.Errorf("IsCorruptionError(%q) = true, want false", msg)
		}
	}
	if IsCorruptionError(nil) {
		t.Error("IsCorruptionError(nil) = true, want false")
	}
}

func TestReportLatchesAndAlertsOnce(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close() //nolint:errcheck // test cleanup

	alerts := make(chan IntegrityAlert, 8)
	handler := func(a IntegrityAlert) { alerts <- a }
	d.corrupt.handler.Store(&handler)

	// An ordinary error must not trip anything.
	d.Report(errors.New("database is locked (5)"))
	if d.Corrupted() {
		t.Fatal("a busy error tripped the corruption latch")
	}

	d.Report(errors.New("database disk image is malformed (11)"))
	if !d.Corrupted() {
		t.Fatal("Corrupted() = false after a corruption error")
	}

	select {
	case a := <-alerts:
		if a.Source != "live traffic" {
			t.Errorf("alert source = %q, want %q", a.Source, "live traffic")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no alert delivered within 2s")
	}

	// A corrupt database errors continuously; the cooldown must keep that from
	// becoming one mail per charge.
	for range 20 {
		d.Report(errors.New("file is not a database (26)"))
	}
	select {
	case a := <-alerts:
		t.Fatalf("cooldown breached: got a second alert %+v", a)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRunIntegrityChecksStopsOnContextCancel(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close() //nolint:errcheck // test cleanup

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.RunIntegrityChecks(ctx, 50*time.Millisecond, nil)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunIntegrityChecks did not return after context cancel")
	}
}
