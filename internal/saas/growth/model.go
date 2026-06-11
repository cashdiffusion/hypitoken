package growth

import "time"

// Channel is one marketing referral link. The slug is the ?ref= value; bonus
// is the USD credit granted to a user who signs up through it.
type Channel struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	BonusUSD    float64   `json:"bonus_usd"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ChannelParams is the writable subset of a Channel, used for create/update.
type ChannelParams struct {
	Slug        string
	Name        string
	Description string
	BonusUSD    float64
	Enabled     bool
}

// ChannelStats is the per-channel analytics rollup shown in the admin tab. It
// joins the channel's visits and referrals with the wallet ledger so the
// operator can compare cost (bonus paid out) against return (what those
// referred users actually topped up and spent).
type ChannelStats struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Enabled     bool    `json:"enabled"`
	BonusUSD    float64 `json:"bonus_usd"`     // current per-signup bonus setting
	Visitors    int64   `json:"visitors"`      // unique anonymous visitors
	Signups     int64   `json:"signups"`       // users credited to this channel
	ConversionR float64 `json:"conversion"`    // signups / visitors (0..1)
	AvgDwellMS  int64   `json:"avg_dwell_ms"`  // mean dwell time across visits
	BonusPaid   float64 `json:"bonus_paid"`    // total bonus granted (cost)
	ToppedUpUSD float64 `json:"topped_up_usd"` // lifetime top-ups by referred users
	SpentUSD    float64 `json:"spent_usd"`     // lifetime charges by referred users
}

// DailyPoint is one day in a channel's visits/signups timeseries (UTC).
type DailyPoint struct {
	Day      string `json:"day"` // YYYY-MM-DD
	Visitors int64  `json:"visitors"`
	Signups  int64  `json:"signups"`
}

// Totals is the headline summary across all channels.
type Totals struct {
	Channels  int64   `json:"channels"`
	Visitors  int64   `json:"visitors"`
	Signups   int64   `json:"signups"`
	BonusPaid float64 `json:"bonus_paid"`
}
