package growth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNotFound is returned when a channel slug/id doesn't exist.
var ErrNotFound = errors.New("channel not found")

const channelCols = `id, slug, name, description, bonus_usd, enabled, created_at, updated_at`

func scanChannel(row interface{ Scan(...any) error }) (*Channel, error) {
	var c Channel
	var enabled int
	var created, updated int64
	if err := row.Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.BonusUSD, &enabled, &created, &updated); err != nil {
		return nil, err
	}
	c.Enabled = enabled != 0
	c.CreatedAt = time.Unix(created, 0)
	c.UpdatedAt = time.Unix(updated, 0)
	return &c, nil
}

// CreateChannel inserts a new referral channel. Slug must already be normalized
// (caller validates via NormalizeSlug). A duplicate slug surfaces as the
// underlying UNIQUE-constraint error.
func (s *Service) CreateChannel(ctx context.Context, p ChannelParams) (*Channel, error) {
	now := time.Now().Unix()
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO marketing_channels (slug, name, description, bonus_usd, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.Slug, p.Name, p.Description, p.BonusUSD, enabled, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetChannel(ctx, id)
}

func (s *Service) GetChannel(ctx context.Context, id int64) (*Channel, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+channelCols+` FROM marketing_channels WHERE id = ?`, id)
	c, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// GetChannelBySlug returns the channel for a ?ref= slug, or ErrNotFound.
func (s *Service) GetChannelBySlug(ctx context.Context, slug string) (*Channel, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+channelCols+` FROM marketing_channels WHERE slug = ?`, slug)
	c, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ListChannels returns every channel, newest first.
func (s *Service) ListChannels(ctx context.Context) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+channelCols+` FROM marketing_channels ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateChannel mutates name/description/bonus/enabled (the slug is immutable
// once links are in the wild — changing it would orphan existing visits).
func (s *Service) UpdateChannel(ctx context.Context, id int64, p ChannelParams) (*Channel, error) {
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE marketing_channels SET name = ?, description = ?, bonus_usd = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.Description, p.BonusUSD, enabled, time.Now().Unix(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetChannel(ctx, id)
}

// DeleteChannel removes a channel. Existing visits/referrals rows keyed by its
// slug are left in place as historical record (analytics still attribute them);
// only the link definition disappears.
func (s *Service) DeleteChannel(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM marketing_channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordVisit upserts a first-touch visit for (slug, visitor). The first hit
// inserts; later hits only push last_seen forward — first_seen is preserved so
// "when did this visitor first arrive" stays accurate. Returns silently if the
// row already exists (ON CONFLICT no-ops the immutable columns).
func (s *Service) RecordVisit(ctx context.Context, slug, visitorID string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO channel_visits (slug, visitor_id, first_seen, last_seen, duration_ms, converted_user_id, created_at)
		 VALUES (?, ?, ?, ?, 0, 0, ?)
		 ON CONFLICT(slug, visitor_id) DO UPDATE SET last_seen = excluded.last_seen`,
		slug, visitorID, now, now, now)
	return err
}

// AccumulateDwell records dwell time for a visit. dwellMS is the visitor's
// total elapsed time on the site this session; we keep the max seen so repeated
// heartbeats (and a final sendBeacon) monotonically raise the figure rather
// than summing overlapping windows. last_seen is refreshed too.
func (s *Service) AccumulateDwell(ctx context.Context, slug, visitorID string, dwellMS int64) error {
	if dwellMS < 0 {
		dwellMS = 0
	}
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`UPDATE channel_visits
		    SET duration_ms = MAX(duration_ms, ?), last_seen = ?
		  WHERE slug = ? AND visitor_id = ?`,
		dwellMS, now, slug, visitorID)
	return err
}

// RecordConversion links a freshly-registered user to the channel and stamps
// the visit (if any) as converted. One channel is credited per user — a second
// call for the same user is a no-op (INSERT OR IGNORE on the user_id PK). The
// returned bool reports whether this was a *new* conversion (true) or a repeat
// (false); callers use it to grant the signup bonus exactly once. Runs in a
// single transaction so the referral row and the visit flag stay consistent.
func (s *Service) RecordConversion(ctx context.Context, userID int64, slug, visitorID string, bonusUSD float64) (inserted bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op
	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO channel_referrals (user_id, slug, bonus_usd, visitor_id, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		userID, slug, bonusUSD, visitorID, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	inserted = n > 0
	if inserted && visitorID != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE channel_visits SET converted_user_id = ? WHERE slug = ? AND visitor_id = ? AND converted_user_id = 0`,
			userID, slug, visitorID); err != nil {
			return false, err
		}
	}
	return inserted, tx.Commit()
}

// Stats computes the per-channel analytics rollup for every channel. Visits and
// referrals aggregate from the growth tables; ROI (topped-up / spent) joins
// channel_referrals → wallet_tx so it reflects real money movement by referred
// users. Channels with zero activity still appear (LEFT JOINs) so the operator
// sees a freshly-created link at 0/0.
func (s *Service) Stats(ctx context.Context) ([]*ChannelStats, error) {
	// Per-channel visit + dwell aggregates.
	visitAgg := map[string]struct {
		visitors int64
		dwellSum int64
		dwellN   int64
	}{}
	{
		rows, err := s.db.QueryContext(ctx,
			`SELECT slug, COUNT(*), COALESCE(SUM(duration_ms),0), COALESCE(SUM(CASE WHEN duration_ms>0 THEN 1 ELSE 0 END),0)
			   FROM channel_visits GROUP BY slug`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var slug string
			var visitors, dwellSum, dwellN int64
			if err := rows.Scan(&slug, &visitors, &dwellSum, &dwellN); err != nil {
				rows.Close()
				return nil, err
			}
			visitAgg[slug] = struct {
				visitors int64
				dwellSum int64
				dwellN   int64
			}{visitors, dwellSum, dwellN}
		}
		rows.Close()
	}

	// Per-channel signups + bonus paid, plus ROI from the wallet ledger. We sum
	// topups/charges over wallet_tx rows belonging to users referred by each
	// channel. amount_usd is signed (+topup / -charge), hence the CASE/-.
	type refAgg struct {
		signups   int64
		bonusPaid float64
		toppedUp  float64
		spent     float64
	}
	refs := map[string]*refAgg{}
	{
		rows, err := s.db.QueryContext(ctx,
			`SELECT r.slug,
			        COUNT(DISTINCT r.user_id),
			        COALESCE(SUM(r.bonus_usd),0)
			   FROM channel_referrals r
			  GROUP BY r.slug`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var slug string
			var a refAgg
			if err := rows.Scan(&slug, &a.signups, &a.bonusPaid); err != nil {
				rows.Close()
				return nil, err
			}
			refs[slug] = &a
		}
		rows.Close()
	}
	{
		// ROI join kept separate so a missing wallet_tx row never drops a
		// referral from the signup count above.
		rows, err := s.db.QueryContext(ctx,
			`SELECT r.slug,
			        COALESCE(SUM(CASE WHEN w.kind='topup'  THEN  w.amount_usd ELSE 0 END),0),
			        COALESCE(SUM(CASE WHEN w.kind='charge' THEN -w.amount_usd ELSE 0 END),0)
			   FROM channel_referrals r
			   JOIN wallet_tx w ON w.user_id = r.user_id
			  GROUP BY r.slug`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var slug string
			var topped, spent float64
			if err := rows.Scan(&slug, &topped, &spent); err != nil {
				rows.Close()
				return nil, err
			}
			if a := refs[slug]; a != nil {
				a.toppedUp = topped
				a.spent = spent
			} else {
				refs[slug] = &refAgg{toppedUp: topped, spent: spent}
			}
		}
		rows.Close()
	}

	channels, err := s.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*ChannelStats, 0, len(channels))
	for _, ch := range channels {
		st := &ChannelStats{
			Slug:     ch.Slug,
			Name:     ch.Name,
			Enabled:  ch.Enabled,
			BonusUSD: ch.BonusUSD,
		}
		if v, ok := visitAgg[ch.Slug]; ok {
			st.Visitors = v.visitors
			if v.dwellN > 0 {
				st.AvgDwellMS = v.dwellSum / v.dwellN
			}
		}
		if a := refs[ch.Slug]; a != nil {
			st.Signups = a.signups
			st.BonusPaid = a.bonusPaid
			st.ToppedUpUSD = a.toppedUp
			st.SpentUSD = a.spent
		}
		if st.Visitors > 0 {
			st.ConversionR = float64(st.Signups) / float64(st.Visitors)
		}
		out = append(out, st)
	}
	return out, nil
}

// Totals returns the headline summary across all channels.
func (s *Service) Totals(ctx context.Context) (*Totals, error) {
	t := &Totals{}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM marketing_channels`).Scan(&t.Channels); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_visits`).Scan(&t.Visitors); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(bonus_usd),0) FROM channel_referrals`).Scan(&t.Signups, &t.BonusPaid); err != nil {
		return nil, err
	}
	return t, nil
}

// Timeseries returns the last `days` of per-day visit and signup counts across
// all channels (UTC), oldest first, with gaps zero-filled. Powers the trend
// chart in the admin tab.
func (s *Service) Timeseries(ctx context.Context, days int) ([]*DailyPoint, error) {
	if days <= 0 {
		days = 14
	}
	now := time.Now()
	points := make([]*DailyPoint, days)
	idx := map[string]int{}
	for i := range days {
		d := now.Add(time.Duration(-(days - 1 - i)) * 24 * time.Hour).UTC().Format("2006-01-02")
		points[i] = &DailyPoint{Day: d}
		idx[d] = i
	}
	from := now.Add(-time.Duration(days) * 24 * time.Hour).Unix()

	visitRows, err := s.db.QueryContext(ctx,
		`SELECT strftime('%Y-%m-%d', first_seen, 'unixepoch') AS day, COUNT(*)
		   FROM channel_visits WHERE first_seen >= ? GROUP BY day`, from)
	if err != nil {
		return nil, err
	}
	for visitRows.Next() {
		var day string
		var n int64
		if err := visitRows.Scan(&day, &n); err != nil {
			visitRows.Close()
			return nil, err
		}
		if i, ok := idx[day]; ok {
			points[i].Visitors = n
		}
	}
	visitRows.Close()

	signupRows, err := s.db.QueryContext(ctx,
		`SELECT strftime('%Y-%m-%d', created_at, 'unixepoch') AS day, COUNT(*)
		   FROM channel_referrals WHERE created_at >= ? GROUP BY day`, from)
	if err != nil {
		return nil, err
	}
	for signupRows.Next() {
		var day string
		var n int64
		if err := signupRows.Scan(&day, &n); err != nil {
			signupRows.Close()
			return nil, err
		}
		if i, ok := idx[day]; ok {
			points[i].Signups = n
		}
	}
	signupRows.Close()

	return points, nil
}
