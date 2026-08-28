package admin

import (
	"math"
	"testing"
	"time"

	"github.com/wjsoj/cc-core/requestlog"
)

// TestRedactPublicResultDropsMagnitudes locks the guarantee the whole file
// exists for: nothing a customer receives may carry an absolute count, a
// dollar figure, or a per-model / per-client breakdown.
func TestRedactPublicResultDropsMagnitudes(t *testing.T) {
	res := &requestlog.Result{
		Summary: requestlog.Aggregate{
			Count: 812_345, InputTokens: 4_000_000, OutputTokens: 1_000_000,
			CacheReadTokens: 15_000_000, CacheCreateTokens: 1_000_000,
			CostUSD: 9_412.55, BilledUSD: 2_823.76, Errors: 91,
		},
		ByClient: map[string]requestlog.Aggregate{"Alice": {Count: 500_000, CostUSD: 7_000}},
		ByModel:  map[string]requestlog.Aggregate{"claude-opus-5": {Count: 400_000, CostUSD: 6_000}},
		ByDay: map[string]requestlog.Aggregate{
			"2026-08-26": {Count: 100_000, InputTokens: 2_000_000, CacheReadTokens: 8_000_000, CostUSD: 3_100},
			"2026-08-27": {Count: 50_000, InputTokens: 1_000_000, CacheReadTokens: 4_000_000, CostUSD: 1_550},
		},
		Entries: make([]requestlog.Record, 3),
		Scanned: 999,
	}
	redactPublicResult(res)

	if res.Entries != nil || res.Scanned != 0 {
		t.Error("raw per-request ledger survived redaction")
	}
	if res.ByClient != nil || res.ByModel != nil {
		t.Error("per-client / per-model breakdown survived redaction")
	}
	if res.Summary.Count != 0 || res.Summary.Errors != 0 {
		t.Errorf("summary still carries counts: %+v", res.Summary)
	}
	if res.Summary.CostUSD != 0 || res.Summary.BilledUSD != 0 {
		t.Errorf("summary still carries spend: %+v", res.Summary)
	}
	for day, a := range res.ByDay {
		if a.CostUSD != 0 || a.BilledUSD != 0 {
			t.Errorf("%s still carries spend: %+v", day, a)
		}
		// Every surviving counter is an index into [0, publicScale], never a
		// real magnitude. The originals here are all six or seven digits.
		for _, v := range []int64{a.Count, a.InputTokens, a.OutputTokens, a.CacheReadTokens, a.CacheCreateTokens} {
			if v > int64(publicScale) {
				t.Errorf("%s: %d exceeds the normalization ceiling — a raw magnitude leaked", day, v)
			}
		}
	}
}

// TestRedactPublicSummaryPreservesHitRate: the cache hit rate is the one
// platform figure the console still prints, so normalization must not move it.
func TestRedactPublicSummaryPreservesHitRate(t *testing.T) {
	in := requestlog.Aggregate{
		InputTokens: 3_141_592, OutputTokens: 653_589,
		CacheReadTokens: 27_182_818, CacheCreateTokens: 2_845_904,
	}
	rate := func(a requestlog.Aggregate) float64 {
		denom := a.InputTokens + a.CacheReadTokens + a.CacheCreateTokens
		if denom == 0 {
			return 0
		}
		return float64(a.CacheReadTokens) / float64(denom)
	}
	want, got := rate(in), rate(redactPublicSummary(in))
	// The console renders two decimals of a percentage, i.e. 1e-4 of the ratio.
	if math.Abs(want-got) > 1e-5 {
		t.Errorf("hit rate drifted through redaction: want %.6f, got %.6f", want, got)
	}
}

// TestRedactPublicByDayPreservesShape: the charts are the deliverable, so the
// relative height of each day must survive even though the values do not.
func TestRedactPublicByDayPreservesShape(t *testing.T) {
	out := redactPublicByDay(map[string]requestlog.Aggregate{
		"2026-08-25": {Count: 25_000, InputTokens: 1_000_000},
		"2026-08-26": {Count: 100_000, InputTokens: 4_000_000},
		"2026-08-27": {Count: 50_000, InputTokens: 2_000_000},
	})
	peak := out["2026-08-26"]
	if peak.Count != int64(publicScale) || peak.InputTokens != int64(publicScale) {
		t.Fatalf("busiest day is not the normalization peak: %+v", peak)
	}
	// A quarter of the peak stays a quarter of the peak.
	if q := out["2026-08-25"]; q.Count != int64(publicScale)/4 || q.InputTokens != int64(publicScale)/4 {
		t.Errorf("shape lost: want a quarter of the peak, got %+v", q)
	}
	if h := out["2026-08-27"]; h.Count != int64(publicScale)/2 {
		t.Errorf("shape lost: want half the peak, got %+v", h)
	}
}

// TestRedactPublicHourlyKeepsTheClock: when traffic happens is not a secret;
// how much of it there is, is. The hour labels must come through untouched.
func TestRedactPublicHourlyKeepsTheClock(t *testing.T) {
	h0 := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	out := redactPublicHourly([]requestlog.HourBucket{
		{Hour: h0, Count: 4_000, InputTokens: 800_000, CostUSD: 120},
		{Hour: h0.Add(time.Hour), Count: 2_000, InputTokens: 400_000, CostUSD: 60},
	})
	if len(out) != 2 || !out[0].Hour.Equal(h0) || !out[1].Hour.Equal(h0.Add(time.Hour)) {
		t.Fatalf("hour labels did not survive: %+v", out)
	}
	if out[0].Count != int64(publicScale) || out[1].Count != int64(publicScale)/2 {
		t.Errorf("shape lost: %+v", out)
	}
	for _, b := range out {
		if b.CostUSD != 0 {
			t.Errorf("hourly spend survived redaction: %+v", b)
		}
	}
}
