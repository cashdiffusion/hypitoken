package analytics

// Overview is the full visitor-behaviour rollup the admin "Growth" tab renders
// below the channel stats: headline totals, a daily traffic trend, and four
// breakdowns (first action, dwell distribution, acquisition source, top flows).
type Overview struct {
	Totals       OverviewTotals `json:"totals"`
	Daily        []*DailyPoint  `json:"daily"`         // per-day traffic, oldest first, zero-filled
	FirstActions []*Bucket      `json:"first_actions"` // what visitors did first ('' → 'bounce')
	DwellBuckets []*Bucket      `json:"dwell_buckets"` // session dwell histogram (fixed bucket order)
	Sources      []*Bucket      `json:"sources"`       // direct | search | social | referral | internal
	Referrers    []*Bucket      `json:"referrers"`     // top external referrer hosts
	Paths        []*PathCount   `json:"paths"`         // top page flows (home → pricing → register)
}

// OverviewTotals is the headline summary across all sessions in the window.
type OverviewTotals struct {
	Sessions      int64   `json:"sessions"`
	Visitors      int64   `json:"visitors"`        // distinct visitor_id
	Pageviews     int64   `json:"pageviews"`       // total pageviews
	BounceRate    float64 `json:"bounce_rate"`     // 0..1 — sessions with no interaction beyond the landing page
	MedianDwellMS int64   `json:"median_dwell_ms"` // median over sessions with dwell > 0
	AvgDwellMS    int64   `json:"avg_dwell_ms"`    // mean over sessions with dwell > 0
}

// DailyPoint is one day in the traffic timeseries (UTC).
type DailyPoint struct {
	Day       string `json:"day"` // YYYY-MM-DD
	Sessions  int64  `json:"sessions"`
	Visitors  int64  `json:"visitors"`
	Pageviews int64  `json:"pageviews"`
}

// Bucket is a labelled count, used for the first-action / dwell / source /
// referrer breakdowns.
type Bucket struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// PathCount is one reconstructed visit flow and how many sessions walked it.
type PathCount struct {
	Path  string `json:"path"`  // "home → pricing → register"
	Count int64  `json:"count"` // sessions following this exact flow
}
