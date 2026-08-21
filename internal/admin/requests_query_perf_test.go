package admin

import (
	"testing"
	"time"

	"github.com/wjsoj/cc-core/requestlog"
)

// The date picker sends whole days, and only the day-label form lets
// requestlog answer from its pre-summed cube. On the production archive that
// is the difference between materialising ~480k rows (3.2-4.0s) and 34ms, so
// a regression here is not a style issue — it is the panel going back to
// taking nine seconds to load.
func TestDayBoundsUseTheCubeEligibleForm(t *testing.T) {
	var f requestlog.Filter
	applyDateBounds(&f, "2026-08-08", "2026-08-21")

	if f.FromDay != "2026-08-08" || f.ToDay != "2026-08-21" {
		t.Fatalf("day labels = %q..%q, want them populated — otherwise the cube is skipped", f.FromDay, f.ToDay)
	}
	if !f.From.IsZero() || !f.To.IsZero() {
		t.Fatalf("timestamps also set (%v..%v); requestlog drops the labels when both forms are present", f.From, f.To)
	}
}

// An arbitrary instant is finer than the cube's day grain, so it must keep the
// scanning form rather than be silently rounded to a different window.
func TestTimestampBoundsStayExact(t *testing.T) {
	var f requestlog.Filter
	applyDateBounds(&f, "2026-08-08T13:45:00Z", "2026-08-21T09:15:00Z")

	if f.FromDay != "" || f.ToDay != "" {
		t.Fatalf("day labels = %q..%q, want empty for an RFC3339 window", f.FromDay, f.ToDay)
	}
	if f.From.IsZero() || f.To.IsZero() {
		t.Fatal("timestamps not set for an RFC3339 window")
	}
	if got := f.From.UTC().Format(time.RFC3339); got != "2026-08-08T13:45:00Z" {
		t.Fatalf("from = %s, want the instant as given", got)
	}
}

// A mixed pair is the trap: requestlog drops the labels when timestamps are
// also present, which would widen the window to whichever half survived. Both
// bounds must collapse to the same form.
func TestMixedBoundsDoNotSilentlyWidenTheWindow(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"day then instant", "2026-08-08", "2026-08-21T09:15:00Z"},
		{"instant then day", "2026-08-08T13:45:00Z", "2026-08-21"},
	} {
		var f requestlog.Filter
		applyDateBounds(&f, tc.from, tc.to)
		if f.FromDay != "" || f.ToDay != "" {
			t.Errorf("%s: labels %q..%q survived alongside timestamps %v..%v — downstream drops them and the window silently widens",
				tc.name, f.FromDay, f.ToDay, f.From, f.To)
		}
		if f.From.IsZero() || f.To.IsZero() {
			t.Errorf("%s: bound lost entirely (%v..%v)", tc.name, f.From, f.To)
		}
	}
}

// Day labels are an ALTERNATIVE to the timestamps, not an addition — with them
// set, From/To are still zero. A cache key built from the timestamps alone is
// therefore identical for every day-bounded window, and one range's aggregates
// get served for another.
func TestCacheKeySeparatesDayWindows(t *testing.T) {
	mk := func(from, to string) string {
		var f requestlog.Filter
		applyDateBounds(&f, from, to)
		return reqCacheKey(f)
	}
	week := mk("2026-08-15", "2026-08-21")
	fortnight := mk("2026-08-08", "2026-08-21")
	if week == fortnight {
		t.Fatalf("a 7-day and a 14-day window share cache key %q", week)
	}
	if week != mk("2026-08-15", "2026-08-21") {
		t.Fatal("the same window produced two different keys; nothing would ever hit cache")
	}
}

// dims narrows the response, so a caller that omits it must keep getting
// everything — otherwise adding the param quietly empties existing panels.
func TestDimsDefaultsToAll(t *testing.T) {
	if got := parseDims(""); got != 0 {
		t.Fatalf("absent dims = %d, want 0 (requestlog reads zero as all)", got)
	}
	if got := parseDims("nonsense,garbage"); got != 0 {
		t.Fatalf("unrecognised dims = %d, want 0 rather than an empty set", got)
	}
	want := requestlog.DimSummary | requestlog.DimByClient | requestlog.DimByModel
	if got := parseDims("summary,by_client,by_model"); got != want {
		t.Fatalf("dims = %d, want %d", got, want)
	}
	if got := parseDims(" Summary , day "); got != requestlog.DimSummary|requestlog.DimByDay {
		t.Fatalf("dims did not tolerate spacing/case: %d", got)
	}
}

// Two filters differing only in dims must not share a cache entry, or a
// narrowed request poisons the cache for a caller that wants everything.
func TestCacheKeySeparatesDims(t *testing.T) {
	a := requestlog.Filter{FromDay: "2026-08-08", ToDay: "2026-08-21"}
	b := a
	b.Dims = requestlog.DimSummary
	if reqCacheKey(a) == reqCacheKey(b) {
		t.Fatal("full and summary-only queries share a cache key")
	}
}
