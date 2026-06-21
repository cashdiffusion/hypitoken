package referral

import (
	"context"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// PersonalStats is a user's own referral scoreboard, shown on the invite page.
type PersonalStats struct {
	Invites        int      `json:"invites"`         // confirmed (non-fraud) invites
	EarnedUSD      float64  `json:"earned_usd"`      // inviter + milestone bonuses credited
	PendingUSD     float64  `json:"pending_usd"`     // deferred inviter rewards owed (first_spend)
	Rank           int      `json:"rank"`            // rank on the invites leaderboard (0 = unranked)
	CurrentTier    *Tier    `json:"current_tier"`    // highest tier reached (nil = none yet)
	NextTier       *Tier    `json:"next_tier"`       // next tier to chase (nil = maxed)
	NextRemaining  int      `json:"next_remaining"`  // invites left to reach NextTier
	UnlockedStyles []string `json:"unlocked_styles"` // card styles unlocked by tiers
}

// PersonalStats computes a user's referral scoreboard from the active campaign's
// tier ladder.
func (s *Service) PersonalStats(ctx context.Context, userID int64) (*PersonalStats, error) {
	st := &PersonalStats{UnlockedStyles: []string{"claude", "openai"}}
	st.Invites = s.countConfirmedInvites(ctx, userID)

	_ = s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount_usd),0) FROM wallet_tx WHERE user_id = ? AND (ref LIKE 'ref_inviter:%' OR ref LIKE 'ref_tier:%')`,
		userID).Scan(&st.EarnedUSD)
	_ = s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(inviter_bonus_usd),0) FROM referral_conversions WHERE inviter_user_id = ? AND inviter_paid = 0 AND fraud = 0`,
		userID).Scan(&st.PendingUSD)

	st.Rank, _ = s.DB.RankOf(ctx, userID, db.MetricInvites)

	if camp, err := s.ActiveCampaign(ctx); err == nil {
		tiers, terr := s.ListTiers(ctx, camp.ID)
		if terr == nil {
			styles := map[string]bool{}
			for _, t := range tiers {
				if t.Threshold <= st.Invites {
					cur := t
					st.CurrentTier = cur
					if cur.CardStyleUnlock != "" {
						styles[cur.CardStyleUnlock] = true
					}
				} else if st.NextTier == nil {
					nt := t
					st.NextTier = nt
					st.NextRemaining = nt.Threshold - st.Invites
				}
			}
			if len(styles) > 0 {
				out := []string{"claude", "openai"}
				st.UnlockedStyles = out
			}
		}
	}
	return st, nil
}

// TopReferrer is one row of the operator leaderboard.
type TopReferrer struct {
	UserID    int64   `json:"user_id"`
	Email     string  `json:"email"`
	Invites   int     `json:"invites"`
	EarnedUSD float64 `json:"earned_usd"`
}

// OpsStats is the operator-facing rollup for the admin referral dashboard.
type OpsStats struct {
	TotalUsers    int64          `json:"total_users"`
	CardsMinted   int64          `json:"cards_minted"`
	Impressions   int64          `json:"impressions"`
	Conversions   int64          `json:"conversions"`    // confirmed (non-fraud) invite signups
	FraudBlocked  int64          `json:"fraud_blocked"`  // signups flagged as abuse
	Inviters      int64          `json:"inviters"`       // distinct users who referred ≥1
	PlatformSpend float64        `json:"platform_spend"` // total referral bonus paid (invitee+inviter+tier)
	KFactor       float64        `json:"k_factor"`       // confirmed invites / total users
	GiftTotals    *db.GiftTotals `json:"gift_totals"`
	TopReferrers  []TopReferrer  `json:"top_referrers"`
}

// OpsStats aggregates the referral system for the admin dashboard.
func (s *Service) OpsStats(ctx context.Context) (*OpsStats, error) {
	o := &OpsStats{}
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&o.TotalUsers)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(impressions),0) FROM referral_cards`).Scan(&o.CardsMinted, &o.Impressions)
	_ = s.DB.QueryRowContext(ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN fraud=0 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN fraud=1 THEN 1 ELSE 0 END),0),
			COUNT(DISTINCT CASE WHEN fraud=0 THEN inviter_user_id END)
		 FROM referral_conversions`).Scan(&o.Conversions, &o.FraudBlocked, &o.Inviters)
	_ = s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(-amount_usd),0) FROM wallet_tx WHERE ref LIKE 'ref_invitee:%' OR ref LIKE 'ref_inviter:%' OR ref LIKE 'ref_tier:%'`).Scan(&o.PlatformSpend)
	// amount_usd for these is positive (a credit); the SUM(-amount) above flips
	// sign — undo it so spend reads positive.
	o.PlatformSpend = -o.PlatformSpend

	if o.TotalUsers > 0 {
		o.KFactor = float64(o.Conversions) / float64(o.TotalUsers)
	}
	o.GiftTotals, _ = s.DB.GiftTotalsAll(ctx)

	rows, err := s.DB.QueryContext(ctx, `
		SELECT c.inviter_user_id, COALESCE(u.email,''), COUNT(*) AS invites
		FROM referral_conversions c LEFT JOIN users u ON u.id = c.inviter_user_id
		WHERE c.fraud = 0
		GROUP BY c.inviter_user_id
		ORDER BY invites DESC, c.inviter_user_id ASC
		LIMIT 10`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tr TopReferrer
			if err := rows.Scan(&tr.UserID, &tr.Email, &tr.Invites); err != nil {
				break
			}
			_ = s.DB.QueryRowContext(ctx,
				`SELECT COALESCE(SUM(amount_usd),0) FROM wallet_tx WHERE user_id = ? AND (ref LIKE 'ref_inviter:%' OR ref LIKE 'ref_tier:%')`,
				tr.UserID).Scan(&tr.EarnedUSD)
			o.TopReferrers = append(o.TopReferrers, tr)
		}
	}
	if o.TopReferrers == nil {
		o.TopReferrers = []TopReferrer{}
	}
	return o, nil
}
