package referral

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNotFound is returned when a campaign / tier lookup misses.
var ErrNotFound = errors.New("not found")

// Campaign is an operator-configurable referral activity. The bonus amounts,
// gift expiry, caps, and A/B copy all live here so the behaviour is tunable from
// the admin panel without a code change.
type Campaign struct {
	ID                 int64   `json:"id"`
	Slug               string  `json:"slug"`
	Name               string  `json:"name"`
	Kind               string  `json:"kind"`   // invite | gift | both
	Status             string  `json:"status"` // active | paused | ended
	InviteeBonusUSD    float64 `json:"invitee_bonus_usd"`
	InviterBonusUSD    float64 `json:"inviter_bonus_usd"`
	InviterRewardOn    string  `json:"inviter_reward_on"` // signup | first_spend
	GiftExpiryDays     int     `json:"gift_expiry_days"`
	MaxGiftUSD         float64 `json:"max_gift_usd"`
	MaxRewardedInvites int     `json:"max_rewarded_invites"` // 0 = unlimited
	DailyBudgetUSD     float64 `json:"daily_budget_usd"`     // circuit breaker; 0 = unlimited
	// MinInviteeSpendUSD is how much the invitee must actually burn before a
	// deferred (reward_on=first_spend) inviter reward is released. Without it
	// "first spend" is satisfied by a $0.000005 charge, which is no barrier at
	// all to someone farming invites. 0 = any spend releases.
	MinInviteeSpendUSD float64 `json:"min_invitee_spend_usd"`
	// DailyInviteCap bounds how many confirmed invites one inviter can be paid
	// for in a rolling 24h. Past it the conversion is still attributed (the
	// invitee keeps their bonus) but the inviter earns nothing, so a farm's
	// yield per identity is capped even when no per-signup rule fires.
	// 0 = unlimited.
	DailyInviteCap int       `json:"daily_invite_cap"`
	StartsAt       int64     `json:"starts_at"`
	EndsAt         int64     `json:"ends_at"`
	Headline       string    `json:"headline"`
	Subcopy        string    `json:"subcopy"`
	VariantB       string    `json:"variant_b"` // JSON {headline,subcopy}, optional
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Tier is one milestone rung: reaching `threshold` confirmed invites unlocks the
// tier (its badge / card style) and grants a one-off bonus.
type Tier struct {
	ID              int64     `json:"id"`
	CampaignID      int64     `json:"campaign_id"`
	Threshold       int       `json:"threshold"`
	TierName        string    `json:"tier_name"`
	CardStyleUnlock string    `json:"card_style_unlock"`
	BonusUSD        float64   `json:"bonus_usd"`
	Badge           string    `json:"badge"`
	CreatedAt       time.Time `json:"created_at"`
}

const campaignCols = `id, slug, name, kind, status, invitee_bonus_usd, inviter_bonus_usd, inviter_reward_on, gift_expiry_days, max_gift_usd, max_rewarded_invites, daily_budget_usd, min_invitee_spend_usd, daily_invite_cap, starts_at, ends_at, headline, subcopy, variant_b, created_at, updated_at`

func scanCampaign(row interface{ Scan(...any) error }) (*Campaign, error) {
	var c Campaign
	var created, updated int64
	if err := row.Scan(&c.ID, &c.Slug, &c.Name, &c.Kind, &c.Status, &c.InviteeBonusUSD, &c.InviterBonusUSD,
		&c.InviterRewardOn, &c.GiftExpiryDays, &c.MaxGiftUSD, &c.MaxRewardedInvites, &c.DailyBudgetUSD,
		&c.MinInviteeSpendUSD, &c.DailyInviteCap, &c.StartsAt, &c.EndsAt,
		&c.Headline, &c.Subcopy, &c.VariantB, &created, &updated); err != nil {
		return nil, err
	}
	c.CreatedAt = time.Unix(created, 0)
	c.UpdatedAt = time.Unix(updated, 0)
	return &c, nil
}

// activeNow reports whether the campaign is live right now (status active and
// within any configured start/end window).
func (c *Campaign) activeNow() bool {
	if c.Status != "active" {
		return false
	}
	now := time.Now().Unix()
	if c.StartsAt > 0 && now < c.StartsAt {
		return false
	}
	if c.EndsAt > 0 && now > c.EndsAt {
		return false
	}
	return true
}

// ActiveCampaign returns the currently-live campaign the in-app UI should drive.
// Prefers an active, in-window campaign (newest first); falls back to the
// 'default' seed so the page always has copy + bonus amounts to show.
func (s *Service) ActiveCampaign(ctx context.Context) (*Campaign, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+campaignCols+` FROM referral_campaigns ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var fallback *Campaign
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, err
		}
		if c.activeNow() {
			return c, nil
		}
		if fallback == nil || c.Slug == "default" {
			fallback = c
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if fallback == nil {
		return nil, ErrNotFound
	}
	return fallback, nil
}

// GetCampaign returns one campaign by id.
func (s *Service) GetCampaign(ctx context.Context, id int64) (*Campaign, error) {
	c, err := scanCampaign(s.DB.QueryRowContext(ctx, `SELECT `+campaignCols+` FROM referral_campaigns WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ListCampaigns returns every campaign, newest first.
func (s *Service) ListCampaigns(ctx context.Context) ([]*Campaign, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+campaignCols+` FROM referral_campaigns ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Campaign
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CampaignParams is the mutable set for create/update.
type CampaignParams struct {
	Slug               string
	Name               string
	Kind               string
	Status             string
	InviteeBonusUSD    float64
	InviterBonusUSD    float64
	InviterRewardOn    string
	GiftExpiryDays     int
	MaxGiftUSD         float64
	MaxRewardedInvites int
	DailyBudgetUSD     float64
	MinInviteeSpendUSD float64
	DailyInviteCap     int
	StartsAt           int64
	EndsAt             int64
	Headline           string
	Subcopy            string
	VariantB           string
}

func normalizeKind(k string) string {
	switch k {
	case "invite", "gift", "both":
		return k
	default:
		return "both"
	}
}

func normalizeStatus(st string) string {
	switch st {
	case "active", "paused", "ended":
		return st
	default:
		return "active"
	}
}

func normalizeRewardOn(r string) string {
	if r == "first_spend" {
		return "first_spend"
	}
	return "signup"
}

// CreateCampaign inserts a new campaign.
func (s *Service) CreateCampaign(ctx context.Context, p CampaignParams) (*Campaign, error) {
	now := time.Now().Unix()
	if p.GiftExpiryDays <= 0 {
		p.GiftExpiryDays = 30
	}
	if p.MaxGiftUSD <= 0 {
		p.MaxGiftUSD = 100
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO referral_campaigns
		(slug, name, kind, status, invitee_bonus_usd, inviter_bonus_usd, inviter_reward_on, gift_expiry_days, max_gift_usd, max_rewarded_invites, daily_budget_usd, min_invitee_spend_usd, daily_invite_cap, starts_at, ends_at, headline, subcopy, variant_b, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Slug, p.Name, normalizeKind(p.Kind), normalizeStatus(p.Status), p.InviteeBonusUSD, p.InviterBonusUSD,
		normalizeRewardOn(p.InviterRewardOn), p.GiftExpiryDays, p.MaxGiftUSD, p.MaxRewardedInvites, p.DailyBudgetUSD,
		p.MinInviteeSpendUSD, p.DailyInviteCap, p.StartsAt, p.EndsAt, p.Headline, p.Subcopy, p.VariantB, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetCampaign(ctx, id)
}

// UpdateCampaign mutates an existing campaign (slug is immutable).
func (s *Service) UpdateCampaign(ctx context.Context, id int64, p CampaignParams) (*Campaign, error) {
	if _, err := s.GetCampaign(ctx, id); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if p.GiftExpiryDays <= 0 {
		p.GiftExpiryDays = 30
	}
	if p.MaxGiftUSD <= 0 {
		p.MaxGiftUSD = 100
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE referral_campaigns SET
		name=?, kind=?, status=?, invitee_bonus_usd=?, inviter_bonus_usd=?, inviter_reward_on=?,
		gift_expiry_days=?, max_gift_usd=?, max_rewarded_invites=?, daily_budget_usd=?, min_invitee_spend_usd=?,
		daily_invite_cap=?, starts_at=?, ends_at=?, headline=?, subcopy=?, variant_b=?, updated_at=? WHERE id=?`,
		p.Name, normalizeKind(p.Kind), normalizeStatus(p.Status), p.InviteeBonusUSD, p.InviterBonusUSD,
		normalizeRewardOn(p.InviterRewardOn), p.GiftExpiryDays, p.MaxGiftUSD, p.MaxRewardedInvites, p.DailyBudgetUSD,
		p.MinInviteeSpendUSD, p.DailyInviteCap, p.StartsAt, p.EndsAt, p.Headline, p.Subcopy, p.VariantB, now, id); err != nil {
		return nil, err
	}
	return s.GetCampaign(ctx, id)
}

// ListTiers returns a campaign's milestone tiers ordered by threshold.
func (s *Service) ListTiers(ctx context.Context, campaignID int64) ([]*Tier, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, campaign_id, threshold, tier_name, card_style_unlock, bonus_usd, badge, created_at
		FROM referral_tiers WHERE campaign_id = ? ORDER BY threshold ASC`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Tier
	for rows.Next() {
		var t Tier
		var created int64
		if err := rows.Scan(&t.ID, &t.CampaignID, &t.Threshold, &t.TierName, &t.CardStyleUnlock, &t.BonusUSD, &t.Badge, &created); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(created, 0)
		out = append(out, &t)
	}
	return out, rows.Err()
}

// CreateTier adds a milestone tier to a campaign.
func (s *Service) CreateTier(ctx context.Context, campaignID int64, threshold int, name, cardStyle string, bonusUSD float64, badge string) (*Tier, error) {
	now := time.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO referral_tiers
		(campaign_id, threshold, tier_name, card_style_unlock, bonus_usd, badge, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, campaignID, threshold, name, cardStyle, bonusUSD, badge, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	var t Tier
	row := s.DB.QueryRowContext(ctx, `SELECT id, campaign_id, threshold, tier_name, card_style_unlock, bonus_usd, badge, created_at FROM referral_tiers WHERE id = ?`, id)
	var created int64
	if err := row.Scan(&t.ID, &t.CampaignID, &t.Threshold, &t.TierName, &t.CardStyleUnlock, &t.BonusUSD, &t.Badge, &created); err != nil {
		return nil, err
	}
	t.CreatedAt = time.Unix(created, 0)
	return &t, nil
}

// DeleteTier removes a milestone tier.
func (s *Service) DeleteTier(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM referral_tiers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
