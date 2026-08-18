package analytics_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/wjsoj/CPA-Claude/internal/saas/analytics"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
)

func TestOpenCreatesUsableStandaloneDB(t *testing.T) {
	svc, err := analytics.Open(filepath.Join(t.TempDir(), "analytics.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer svc.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()
	if err := svc.RecordEvent(ctx, "sess-1", "vis-1", "pageview", "/", "/", "direct", ""); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	ov, err := svc.Overview(ctx, 7)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if ov.Totals.Sessions != 1 {
		t.Fatalf("Sessions = %d, want 1", ov.Totals.Sessions)
	}
}

// The split has to carry existing history across, or the Growth tab goes blank
// on the release that separates the files.
func TestImportFromMovesHistoryOnceAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	shared, err := saasdb.Open(filepath.Join(dir, "saas.db"))
	if err != nil {
		t.Fatalf("open saas db: %v", err)
	}
	defer shared.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()
	// Populate the OLD location through a Service bound to the shared handle,
	// exactly how production wrote these rows before the split.
	old := analytics.New(shared.DB)
	for _, sid := range []string{"s1", "s2", "s3"} {
		if err := old.RecordEvent(ctx, sid, "v-"+sid, "pageview", "/", "/", "direct", ""); err != nil {
			t.Fatalf("seed %s: %v", sid, err)
		}
		if err := old.RecordEvent(ctx, sid, "v-"+sid, "action", "start", "/", "direct", ""); err != nil {
			t.Fatalf("seed action %s: %v", sid, err)
		}
	}

	svc, err := analytics.Open(filepath.Join(dir, "analytics.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer svc.Close() //nolint:errcheck // test cleanup

	if err := svc.ImportFrom(ctx, shared.DB); err != nil {
		t.Fatalf("ImportFrom: %v", err)
	}
	ov, err := svc.Overview(ctx, 7)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if ov.Totals.Sessions != 3 {
		t.Fatalf("after import Sessions = %d, want 3", ov.Totals.Sessions)
	}

	// Running again (every restart does) must not duplicate anything.
	if err := svc.ImportFrom(ctx, shared.DB); err != nil {
		t.Fatalf("second ImportFrom: %v", err)
	}
	ov2, err := svc.Overview(ctx, 7)
	if err != nil {
		t.Fatalf("Overview after re-import: %v", err)
	}
	if ov2.Totals.Sessions != 3 {
		t.Fatalf("re-import duplicated rows: Sessions = %d, want 3", ov2.Totals.Sessions)
	}
}

// A fresh install has no source tables at all; that is normal, not an error.
func TestImportFromEmptySourceIsNoOp(t *testing.T) {
	dir := t.TempDir()
	svc, err := analytics.Open(filepath.Join(dir, "analytics.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer svc.Close() //nolint:errcheck // test cleanup

	bare, err := sql.Open("sqlite", filepath.Join(dir, "bare.db"))
	if err != nil {
		t.Fatalf("open bare: %v", err)
	}
	defer bare.Close() //nolint:errcheck // test cleanup

	if err := svc.ImportFrom(context.Background(), bare); err != nil {
		t.Fatalf("ImportFrom on a source with no tables = %v, want nil", err)
	}
	if err := svc.ImportFrom(context.Background(), nil); err != nil {
		t.Fatalf("ImportFrom(nil) = %v, want nil", err)
	}
}
