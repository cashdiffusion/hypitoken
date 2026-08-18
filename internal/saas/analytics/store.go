package analytics

import (
	"context"
	"sort"
	"strings"
	"time"
)

// RecordEvent appends one event to a session and advances its counters, all in
// a single transaction so the session row and its flow log stay consistent.
//
// The first event for a session_id inserts the session (capturing landing page,
// source and referrer — first touch wins via ON CONFLICT DO NOTHING). Then:
//   - a "pageview" bumps pageviews; if it is NOT the landing pageview (the
//     session already had one) and no explicit action came first, it is recorded
//     as the first action "nav:<page>" — the visitor navigated away.
//   - an "action" bumps actions and, if first_action is still unset, becomes it.
//
// first_action stays ” for a session that only ever saw its landing page and
// never interacted — that is precisely a bounce (see Overview).
func (s *Service) RecordEvent(ctx context.Context, sid, vid, kind, name, landingPath, source, domain string) error {
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO web_sessions
		    (session_id, visitor_id, landing_path, first_action, source, referrer_domain, pageviews, actions, duration_ms, started_at, last_seen)
		 VALUES (?, ?, ?, '', ?, ?, 0, 0, 0, ?, ?)
		 ON CONFLICT(session_id) DO NOTHING`,
		sid, vid, landingPath, source, domain, now, now); err != nil {
		return err
	}

	// Next sequence number within the session (1-based, gap-free per session).
	var seq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq),0)+1 FROM web_events WHERE session_id = ?`, sid).Scan(&seq); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO web_events (session_id, visitor_id, kind, name, seq, ts) VALUES (?, ?, ?, ?, ?, ?)`,
		sid, vid, kind, name, seq, now); err != nil {
		return err
	}

	if kind == "pageview" {
		// SQLite evaluates every RHS against the pre-UPDATE row, so `pageviews >= 1`
		// reflects the count *before* this pageview: true only once a landing
		// pageview already exists, i.e. this is a navigation away from it.
		if _, err := tx.ExecContext(ctx,
			`UPDATE web_sessions
			    SET first_action = CASE WHEN first_action = '' AND pageviews >= 1 THEN ? ELSE first_action END,
			        pageviews = pageviews + 1,
			        last_seen = ?
			  WHERE session_id = ?`,
			"nav:"+name, now, sid); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`UPDATE web_sessions
			    SET actions = actions + 1,
			        first_action = CASE WHEN first_action = '' THEN ? ELSE first_action END,
			        last_seen = ?
			  WHERE session_id = ?`,
			name, now, sid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AccumulateDwell records dwell time for a session. dwellMS is the visitor's
// total elapsed time this session; we keep the max seen so repeated heartbeats
// (and a final sendBeacon) monotonically raise the figure rather than summing
// overlapping windows. last_seen is refreshed too. A ping for an unknown
// session is a harmless no-op.
func (s *Service) AccumulateDwell(ctx context.Context, sid string, dwellMS int64) error {
	if dwellMS < 0 {
		dwellMS = 0
	}
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`UPDATE web_sessions SET duration_ms = MAX(duration_ms, ?), last_seen = ? WHERE session_id = ?`,
		dwellMS, now, sid)
	return err
}

// dwellBucketOrder fixes the histogram bucket order so the chart renders
// left-to-right regardless of which buckets have rows.
var dwellBucketOrder = []string{"0-10s", "10-30s", "30-60s", "1-3m", "3-10m", "10m+"}

// Overview computes the full visitor-behaviour rollup over the last `days`
// (UTC), the structure the admin tab renders. days is assumed already clamped.
func (s *Service) Overview(ctx context.Context, days int) (*Overview, error) {
	if days <= 0 {
		days = 14
	}
	from := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	ov := &Overview{}

	if err := s.totals(ctx, from, ov); err != nil {
		return nil, err
	}
	if err := s.daily(ctx, from, days, ov); err != nil {
		return nil, err
	}
	var err error
	if ov.FirstActions, err = s.firstActions(ctx, from); err != nil {
		return nil, err
	}
	if ov.DwellBuckets, err = s.dwellBuckets(ctx, from); err != nil {
		return nil, err
	}
	if ov.Sources, err = s.sources(ctx, from); err != nil {
		return nil, err
	}
	if ov.Referrers, err = s.referrers(ctx, from); err != nil {
		return nil, err
	}
	if ov.Paths, err = s.paths(ctx, from); err != nil {
		return nil, err
	}
	return ov, nil
}

func (s *Service) totals(ctx context.Context, from int64, ov *Overview) error {
	var bounced, dwellSum, dwellN int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COUNT(DISTINCT visitor_id),
		        COALESCE(SUM(pageviews),0),
		        COALESCE(SUM(CASE WHEN actions=0 AND pageviews<=1 THEN 1 ELSE 0 END),0),
		        -- CAST for the same reason as growth.Stats: an INTEGER column can
		        -- still hold a REAL, and SUM would then not Scan into int64.
		        CAST(COALESCE(SUM(duration_ms),0) AS INTEGER),
		        COALESCE(SUM(CASE WHEN duration_ms>0 THEN 1 ELSE 0 END),0)
		   FROM web_sessions WHERE started_at >= ?`, from).
		Scan(&ov.Totals.Sessions, &ov.Totals.Visitors, &ov.Totals.Pageviews, &bounced, &dwellSum, &dwellN); err != nil {
		return err
	}
	if ov.Totals.Sessions > 0 {
		ov.Totals.BounceRate = float64(bounced) / float64(ov.Totals.Sessions)
	}
	if dwellN > 0 {
		ov.Totals.AvgDwellMS = dwellSum / dwellN
	}
	// Median dwell over sessions with dwell > 0: pick the middle row by offset.
	// Two-arg `from` because the subquery counts the same filtered set.
	_ = s.db.QueryRowContext(ctx,
		`SELECT duration_ms FROM web_sessions
		  WHERE started_at >= ? AND duration_ms > 0
		  ORDER BY duration_ms
		  LIMIT 1 OFFSET (SELECT COUNT(*) FROM web_sessions WHERE started_at >= ? AND duration_ms > 0) / 2`,
		from, from).Scan(&ov.Totals.MedianDwellMS)
	return nil
}

func (s *Service) daily(ctx context.Context, from int64, days int, ov *Overview) error {
	now := time.Now()
	points := make([]*DailyPoint, days)
	idx := map[string]int{}
	for i := range days {
		d := now.Add(time.Duration(-(days - 1 - i)) * 24 * time.Hour).UTC().Format("2006-01-02")
		points[i] = &DailyPoint{Day: d}
		idx[d] = i
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT strftime('%Y-%m-%d', started_at, 'unixepoch') AS day,
		        COUNT(*), COUNT(DISTINCT visitor_id), COALESCE(SUM(pageviews),0)
		   FROM web_sessions WHERE started_at >= ? GROUP BY day`, from)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var day string
		var sess, vis, pv int64
		if err := rows.Scan(&day, &sess, &vis, &pv); err != nil {
			return err
		}
		if i, ok := idx[day]; ok {
			points[i].Sessions = sess
			points[i].Visitors = vis
			points[i].Pageviews = pv
		}
	}
	ov.Daily = points
	return rows.Err()
}

func (s *Service) firstActions(ctx context.Context, from int64) ([]*Bucket, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT CASE WHEN first_action = '' THEN 'bounce' ELSE first_action END AS act, COUNT(*)
		   FROM web_sessions WHERE started_at >= ? GROUP BY act ORDER BY COUNT(*) DESC`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBuckets(rows)
}

func (s *Service) dwellBuckets(ctx context.Context, from int64) ([]*Bucket, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT bucket, COUNT(*) FROM (
		     SELECT CASE
		         WHEN duration_ms < 10000  THEN '0-10s'
		         WHEN duration_ms < 30000  THEN '10-30s'
		         WHEN duration_ms < 60000  THEN '30-60s'
		         WHEN duration_ms < 180000 THEN '1-3m'
		         WHEN duration_ms < 600000 THEN '3-10m'
		         ELSE '10m+' END AS bucket
		       FROM web_sessions WHERE started_at >= ?
		 ) GROUP BY bucket`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		counts[k] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Emit in fixed order, including zero buckets, for a stable histogram.
	out := make([]*Bucket, 0, len(dwellBucketOrder))
	for _, k := range dwellBucketOrder {
		out = append(out, &Bucket{Key: k, Count: counts[k]})
	}
	return out, nil
}

func (s *Service) sources(ctx context.Context, from int64) ([]*Bucket, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source, COUNT(*) FROM web_sessions WHERE started_at >= ? GROUP BY source ORDER BY COUNT(*) DESC`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBuckets(rows)
}

func (s *Service) referrers(ctx context.Context, from int64) ([]*Bucket, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT referrer_domain, COUNT(*) FROM web_sessions
		  WHERE started_at >= ? AND referrer_domain <> '' AND source <> 'internal'
		  GROUP BY referrer_domain ORDER BY COUNT(*) DESC LIMIT 8`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBuckets(rows)
}

const (
	// maxFlowHops caps a reconstructed path so a long browsing session doesn't
	// explode into a near-unique flow that nobody else matches.
	maxFlowHops = 5
	// maxFlowPaths caps how many distinct flows the admin tab shows.
	maxFlowPaths = 12
)

// paths reconstructs each session's pageview sequence and counts the most
// common flows. Consecutive duplicate pages are collapsed (a refresh or a
// re-render isn't a hop); the flow is truncated to maxFlowHops with a trailing
// ellipsis. Reconstruction is done in Go because SQLite can't easily aggregate
// ordered string sequences.
func (s *Service) paths(ctx context.Context, from int64) ([]*PathCount, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.session_id, e.name
		   FROM web_events e
		   JOIN web_sessions s ON s.session_id = e.session_id
		  WHERE s.started_at >= ? AND e.kind = 'pageview'
		  ORDER BY e.session_id, e.seq`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int64{}
	var curSession string
	var hops []string
	flush := func() {
		if len(hops) == 0 {
			return
		}
		path := hops
		if len(path) > maxFlowHops {
			path = append(append([]string{}, path[:maxFlowHops]...), "…")
		}
		counts[strings.Join(path, " → ")]++
		hops = hops[:0]
	}
	for rows.Next() {
		var sid, name string
		if err := rows.Scan(&sid, &name); err != nil {
			return nil, err
		}
		if sid != curSession {
			flush()
			curSession = sid
		}
		if n := len(hops); n == 0 || hops[n-1] != name { // collapse consecutive dups
			hops = append(hops, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	flush()

	return topPaths(counts, maxFlowPaths), nil
}

// topPaths sorts a flow→count map and returns the top n, ties broken by path
// string so the output is deterministic (and tests are stable).
func topPaths(counts map[string]int64, n int) []*PathCount {
	out := make([]*PathCount, 0, len(counts))
	for p, c := range counts {
		out = append(out, &PathCount{Path: p, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func scanBuckets(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*Bucket, error) {
	var out []*Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.Key, &b.Count); err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
