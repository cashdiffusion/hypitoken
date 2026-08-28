package admin

import "github.com/wjsoj/cc-core/requestlog"

// The /app/console platform tab is visible to every signed-in customer, and
// the aggregates behind it ARE the business: daily request volume, daily
// spend, which model earns what, how big the largest account is. Hiding those
// in the React layer alone is cosmetic — the JSON is one devtools panel away —
// so a non-operator caller must never receive an absolute magnitude at all.
//
// What it receives instead is the SHAPE. Every series is rescaled so its own
// largest point becomes publicScale and every other point keeps its proportion
// to it. The trend charts render exactly as before; the numbers behind them
// are unrecoverable without the scaling constant, which is never sent.
//
// Each axis is normalized against its OWN maximum on purpose. Sharing one
// divisor across tokens / requests / cost would let a caller divide two series
// into each other and recover a real rate (cost per request, tokens per
// request), which is most of the way back to the raw figures.
//
// Ratios that carry no volume information survive untouched — a cache hit rate
// is a percentage of a hidden total, so it is still hidden.

const (
	// publicScale is the unitless ceiling every redacted timeseries is
	// normalized to. Only relative height across points is meaningful.
	publicScale = 1000.0
	// publicRatioScale normalizes the lifetime token mix. It is far coarser
	// than it looks: the console renders the hit rate to two decimals, so the
	// mix needs ~1e-4 of resolution to round-trip without visible drift.
	publicRatioScale = 1_000_000.0
)

func aggTokens(a requestlog.Aggregate) int64 {
	return a.InputTokens + a.OutputTokens + a.CacheReadTokens + a.CacheCreateTokens
}

// scaleInt maps v onto [0, scale] relative to peak. peak <= 0 means the whole
// series is empty, which stays empty — the console keys its "no traffic in
// this window" placeholder off a zero total.
func scaleInt(v, peak int64, scale float64) int64 {
	if peak <= 0 || v <= 0 {
		return 0
	}
	return int64(float64(v) / float64(peak) * scale)
}

// redactPublicResult strips a request-log query down to what a customer may
// see: normalized per-day shapes, plus a lifetime token mix that preserves the
// cache hit rate. Per-client and per-model breakdowns are dropped outright —
// no rescaling makes "which model earns us what" safe to publish.
func redactPublicResult(res *requestlog.Result) {
	res.Entries = nil
	res.Scanned = 0
	res.ByClient = nil
	res.ByModel = nil
	res.Summary = redactPublicSummary(res.Summary)
	res.ByDay = redactPublicByDay(res.ByDay)
}

// redactPublicSummary keeps only the four token counters, rescaled to a fixed
// total so their ratios (and therefore the cache hit rate) stay exact while
// the volume they were drawn from disappears. Request count, spend and error
// totals are zeroed — nothing on the public console reads them.
func redactPublicSummary(a requestlog.Aggregate) requestlog.Aggregate {
	total := aggTokens(a)
	return requestlog.Aggregate{
		InputTokens:       scaleInt(a.InputTokens, total, publicRatioScale),
		OutputTokens:      scaleInt(a.OutputTokens, total, publicRatioScale),
		CacheReadTokens:   scaleInt(a.CacheReadTokens, total, publicRatioScale),
		CacheCreateTokens: scaleInt(a.CacheCreateTokens, total, publicRatioScale),
	}
}

// redactPublicByDay normalizes the daily series. Tokens are divided by the
// busiest day's token total (so the four types keep their within-day mix and
// the stacked area still stacks correctly) and request count by the busiest
// day's count. Cost is not rescaled but dropped: the public console shows no
// spend chart at all, so there is no reason to ship even a shape of revenue.
func redactPublicByDay(in map[string]requestlog.Aggregate) map[string]requestlog.Aggregate {
	if len(in) == 0 {
		return in
	}
	var maxTokens, maxCount int64
	for _, a := range in {
		if t := aggTokens(a); t > maxTokens {
			maxTokens = t
		}
		if a.Count > maxCount {
			maxCount = a.Count
		}
	}
	out := make(map[string]requestlog.Aggregate, len(in))
	for day, a := range in {
		out[day] = requestlog.Aggregate{
			InputTokens:       scaleInt(a.InputTokens, maxTokens, publicScale),
			OutputTokens:      scaleInt(a.OutputTokens, maxTokens, publicScale),
			CacheReadTokens:   scaleInt(a.CacheReadTokens, maxTokens, publicScale),
			CacheCreateTokens: scaleInt(a.CacheCreateTokens, maxTokens, publicScale),
			Count:             scaleInt(a.Count, maxCount, publicScale),
		}
	}
	return out
}

// redactPublicHourly is redactPublicByDay for the 24h pulse: same two
// divisors, same dropped cost, applied over the hour buckets. The Hour label
// itself is kept — when traffic happens is not a secret, how much is.
func redactPublicHourly(in []requestlog.HourBucket) []requestlog.HourBucket {
	var maxTokens, maxCount int64
	for _, b := range in {
		t := b.InputTokens + b.OutputTokens + b.CacheReadTokens + b.CacheCreateTokens
		if t > maxTokens {
			maxTokens = t
		}
		if b.Count > maxCount {
			maxCount = b.Count
		}
	}
	out := make([]requestlog.HourBucket, len(in))
	for i, b := range in {
		out[i] = requestlog.HourBucket{
			Hour:              b.Hour,
			InputTokens:       scaleInt(b.InputTokens, maxTokens, publicScale),
			OutputTokens:      scaleInt(b.OutputTokens, maxTokens, publicScale),
			CacheReadTokens:   scaleInt(b.CacheReadTokens, maxTokens, publicScale),
			CacheCreateTokens: scaleInt(b.CacheCreateTokens, maxTokens, publicScale),
			Count:             scaleInt(b.Count, maxCount, publicScale),
		}
	}
	return out
}
