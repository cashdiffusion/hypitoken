package analytics_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wjsoj/CPA-Claude/internal/saas/analytics"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// openTestDB spins up a fresh SaaS SQLite DB (migrations v1..vN, including the
// v7 analytics tables) in a temp dir and returns an analytics service over it.
func openTestDB(t *testing.T) *analytics.Service {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return analytics.New(store.DB)
}

// ev is a tiny helper to record a pageview/action and fail the test on error.
func ev(t *testing.T, svc *analytics.Service, sid, vid, kind, name, path, ref string) {
	t.Helper()
	if err := svc.RecordEvent(context.Background(), sid, vid, kind, name, path, ref, ""); err != nil {
		t.Fatalf("record event: %v", err)
	}
}

// TestBounceVsNavVsAction covers the three first-action outcomes plus the
// idempotent session creation (one row per session_id across many events).
func TestBounceVsNavVsAction(t *testing.T) {
	ctx := context.Background()
	svc := openTestDB(t)

	// s1: landing only → bounce.
	ev(t, svc, "s1", "v1", "pageview", "home", "/", "direct")
	// s2: landing then navigates to pricing → first action nav:pricing.
	ev(t, svc, "s2", "v2", "pageview", "home", "/", "direct")
	ev(t, svc, "s2", "v2", "pageview", "pricing", "/", "direct")
	// s3: landing then explicit Start click → first action 'start'.
	ev(t, svc, "s3", "v3", "pageview", "home", "/", "direct")
	ev(t, svc, "s3", "v3", "action", "start", "/", "direct")
	// s3 then also navigates — must NOT overwrite the explicit first action.
	ev(t, svc, "s3", "v3", "pageview", "register", "/", "direct")

	ov, err := svc.Overview(ctx, 14)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if ov.Totals.Sessions != 3 {
		t.Fatalf("want 3 sessions, got %d", ov.Totals.Sessions)
	}
	if ov.Totals.Visitors != 3 {
		t.Fatalf("want 3 visitors, got %d", ov.Totals.Visitors)
	}

	fa := bucketMap(ov.FirstActions)
	if fa["bounce"] != 1 {
		t.Fatalf("want 1 bounce, got %d (%v)", fa["bounce"], fa)
	}
	if fa["nav:pricing"] != 1 {
		t.Fatalf("want 1 nav:pricing, got %d (%v)", fa["nav:pricing"], fa)
	}
	if fa["start"] != 1 {
		t.Fatalf("want 1 start (explicit action wins over later nav), got %d (%v)", fa["start"], fa)
	}

	// Exactly one session bounced (s1), so bounce_rate = 1/3.
	if got := ov.Totals.BounceRate; got < 0.33 || got > 0.34 {
		t.Fatalf("want bounce_rate ~0.333, got %v", got)
	}
}

// TestDwellMaxAndMedian verifies dwell accumulation keeps the max and the
// median/avg roll up over sessions with dwell > 0.
func TestDwellMaxAndMedian(t *testing.T) {
	ctx := context.Background()
	svc := openTestDB(t)

	for _, sid := range []string{"a", "b", "c"} {
		ev(t, svc, sid, "v-"+sid, "pageview", "home", "/", "direct")
	}
	// Heartbeats arrive out of order; the max must win.
	if err := svc.AccumulateDwell(ctx, "a", 5_000); err != nil {
		t.Fatal(err)
	}
	if err := svc.AccumulateDwell(ctx, "a", 20_000); err != nil {
		t.Fatal(err)
	}
	if err := svc.AccumulateDwell(ctx, "a", 12_000); err != nil { // lower → ignored
		t.Fatal(err)
	}
	if err := svc.AccumulateDwell(ctx, "b", 40_000); err != nil {
		t.Fatal(err)
	}
	if err := svc.AccumulateDwell(ctx, "c", 60_000); err != nil {
		t.Fatal(err)
	}

	ov, err := svc.Overview(ctx, 14)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	// durations: a=20000, b=40000, c=60000 → median 40000, avg 40000.
	if ov.Totals.MedianDwellMS != 40_000 {
		t.Fatalf("want median 40000, got %d", ov.Totals.MedianDwellMS)
	}
	if ov.Totals.AvgDwellMS != 40_000 {
		t.Fatalf("want avg 40000, got %d", ov.Totals.AvgDwellMS)
	}

	// Dwell histogram: a→10-30s, b→30-60s, c→1-3m (60000 is the boundary into 1-3m).
	db := bucketMap(ov.DwellBuckets)
	if db["10-30s"] != 1 || db["30-60s"] != 1 || db["1-3m"] != 1 {
		t.Fatalf("unexpected dwell histogram: %v", db)
	}
}

// TestSourceAggregation checks the source breakdown rolls up by the stored
// bucket and that the referrers list excludes direct/internal (empty domain).
// classifySource itself is unit-tested white-box in classify_test.go.
func TestSourceAggregation(t *testing.T) {
	ctx := context.Background()
	svc := openTestDB(t)

	// (session, source, referrer_domain) as the handler would have resolved them.
	rows := []struct{ sid, source, domain string }{
		{"se", "search", "www.google.com"},
		{"so", "social", "twitter.com"},
		{"re", "referral", "some-blog.example"},
		{"di", "direct", ""},
	}
	for _, r := range rows {
		if err := svc.RecordEvent(ctx, r.sid, "v-"+r.sid, "pageview", "home", "/", r.source, r.domain); err != nil {
			t.Fatalf("record %s: %v", r.sid, err)
		}
	}

	ov, err := svc.Overview(ctx, 14)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	src := bucketMap(ov.Sources)
	for want, n := range map[string]int64{"search": 1, "social": 1, "referral": 1, "direct": 1} {
		if src[want] != n {
			t.Fatalf("source %q: want %d, got %d (%v)", want, n, src[want], src)
		}
	}
	// Only the three with a non-empty external domain show up as referrers.
	if len(ov.Referrers) != 3 {
		t.Fatalf("want 3 referrer hosts, got %d (%v)", len(ov.Referrers), ov.Referrers)
	}
}

// TestPathReconstruction verifies flows are rebuilt from the event log, that
// two visitors walking the same flow are counted together, and consecutive
// duplicate pages collapse.
func TestPathReconstruction(t *testing.T) {
	ctx := context.Background()
	svc := openTestDB(t)

	// Two sessions walk home → pricing → register.
	for _, sid := range []string{"p1", "p2"} {
		ev(t, svc, sid, "v-"+sid, "pageview", "home", "/", "direct")
		ev(t, svc, sid, "v-"+sid, "pageview", "home", "/", "direct") // dup → collapses
		ev(t, svc, sid, "v-"+sid, "pageview", "pricing", "/", "direct")
		ev(t, svc, sid, "v-"+sid, "pageview", "register", "/", "direct")
	}
	// One session just bounces on home.
	ev(t, svc, "p3", "v-p3", "pageview", "home", "/", "direct")

	ov, err := svc.Overview(ctx, 14)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if len(ov.Paths) == 0 {
		t.Fatal("want at least one reconstructed path")
	}
	top := ov.Paths[0]
	if top.Path != "home → pricing → register" {
		t.Fatalf("want top path 'home → pricing → register', got %q", top.Path)
	}
	if top.Count != 2 {
		t.Fatalf("want top path count 2, got %d", top.Count)
	}
}

func bucketMap(bs []*analytics.Bucket) map[string]int64 {
	m := map[string]int64{}
	for _, b := range bs {
		m[b.Key] = b.Count
	}
	return m
}
