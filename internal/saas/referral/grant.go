package referral

import (
	"context"
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// txKindBonus matches the ledger's "adjust" enum (operator-style credit) so
// referral/milestone bonuses never count as revenue.
const txKindBonus = db.TxKindAdjust

// todayBonusSpend sums platform-funded referral bonuses paid since UTC midnight
// (invitee + inviter + milestone). It is the circuit breaker's running meter.
func (s *Service) todayBonusSpend(ctx context.Context) float64 {
	var v float64
	_ = s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount_usd),0) FROM wallet_tx
		 WHERE kind = ? AND created_at >= strftime('%s','now','start of day')
		   AND (ref LIKE 'ref_invitee:%' OR ref LIKE 'ref_inviter:%' OR ref LIKE 'ref_tier:%')`,
		txKindBonus).Scan(&v)
	return v
}

// capTripped reports whether today's referral-bonus spend has reached the daily
// budget cap. capUSD <= 0 means unlimited (breaker disabled).
func (s *Service) capTripped(ctx context.Context, capUSD float64) bool {
	if capUSD <= 0 {
		return false
	}
	return s.todayBonusSpend(ctx) >= capUSD
}

// GrantSignupBonus implements auth.ReferralGranter. It is called once, right
// after a new account is created, with the ?ref= the browser captured. Two
// outcomes:
//
//   - ref matches a personal invite code → the platform credits BOTH the new
//     user (invitee bonus) and the inviter (inviter bonus, immediately for a
//     signup-reward campaign or deferred to first spend), gated by the shared
//     signup anti-abuse. Returns matched=true so the caller skips the trial.
//   - ref is anything else → delegate to the growth module (admin marketing
//     channels + its own trial-fallback semantics) so existing attribution keeps
//     working untouched.
//
// Errors are returned for the caller to log but must never block registration.
func (s *Service) GrantSignupBonus(ctx context.Context, userID int64, ref, vid, fp, ip string) (bonusUSD float64, channel string, matched, fraud bool, err error) {
	if s == nil {
		return 0, "", false, false, nil
	}
	code := normalizeInviteCode(ref)
	if code != "" {
		if card, cerr := s.cardByCode(ctx, code); cerr == nil {
			return s.grantInvite(ctx, userID, card, fp, ip)
		} else if !errors.Is(cerr, ErrNotFound) {
			return 0, "", false, false, cerr
		}
	}
	// Not a personal invite code — hand off to the growth channel path (which
	// also records the signup device exactly once and owns the trial fallback).
	if s.Channel != nil {
		return s.Channel.GrantSignupBonus(ctx, userID, ref, vid, fp, ip)
	}
	return 0, "", false, false, nil
}

// grantInvite handles a registration that arrived via a personal invite card.
func (s *Service) grantInvite(ctx context.Context, inviteeID int64, card *Card, fp, ip string) (bonusUSD float64, channel string, matched, fraud bool, err error) {
	// Self-invite is impossible (the invitee is a brand-new account), but the
	// shared anti-abuse still catches same-device / same-subnet farming.
	if s.Channel != nil {
		fraud, _, err = s.Channel.RecordSignupDevice(ctx, inviteeID, fp, ip)
	}

	camp, cerr := s.campaignFor(ctx, card.CampaignID)
	if cerr != nil {
		// No campaign to price the bonus — record nothing, let the caller apply
		// the normal trial credit.
		return 0, "", false, fraud, cerr
	}
	inviterID := card.OwnerID

	// A paused/ended campaign still attributes the signup but pays nothing and
	// lets the invitee fall back to the normal trial credit (friendlier than a
	// silent zero). We still record the conversion + bump the inviter's count.
	active := camp.activeNow()
	// Circuit breaker: once the day's platform bonus spend hits the cap, record
	// the conversion but pay nothing — defends platform money against a spike.
	budgetTripped := active && !fraud && s.capTripped(ctx, camp.DailyBudgetUSD)

	inviteeBonus := camp.InviteeBonusUSD
	inviterBonus := camp.InviterBonusUSD
	inviterPaid := 1
	if !active || fraud || budgetTripped {
		inviteeBonus, inviterBonus = 0, 0
	} else {
		if camp.MaxRewardedInvites > 0 && s.countConfirmedInvites(ctx, inviterID) >= camp.MaxRewardedInvites {
			inviterBonus = 0 // inviter hit their rewarded-invite cap; invitee still gets theirs
		}
		if camp.InviterRewardOn == "first_spend" && inviterBonus > 0 {
			inviterPaid = 0 // owed, released when the invitee first spends
		}
	}

	fraudInt := 0
	if fraud {
		fraudInt = 1
	}
	firstTime, ierr := s.insertConversion(ctx, camp.ID, card.Code, inviterID, inviteeID, inviteeBonus, inviterBonus, fraudInt, inviterPaid)
	if ierr != nil {
		return 0, "", false, fraud, ierr
	}
	if !firstTime {
		// Already attributed (idempotent) — nothing more to do.
		return 0, "", active, fraud, nil
	}
	if fraud {
		log.Warnf("referral: invite signup user=%d via code=%s flagged fraud — bonus withheld", inviteeID, card.Code)
		return 0, "", active, true, err
	}
	if budgetTripped {
		log.Warnf("referral: daily bonus budget cap reached — invite signup user=%d recorded but $0 paid (raise daily_budget_usd to resume)", inviteeID)
	}

	if inviteeBonus > 0 {
		if _, berr := s.DB.AddBalance(ctx, inviteeID, txKindBonus, inviteeBonus, "ref_invitee:"+card.Code, "邀请注册赠额", true); berr != nil {
			log.Warnf("referral: credit invitee %d: %v", inviteeID, berr)
		}
	}
	if inviterPaid == 1 && inviterBonus > 0 {
		if _, berr := s.DB.AddBalance(ctx, inviterID, txKindBonus, inviterBonus, "ref_inviter:"+card.Code, "成功邀请奖励", true); berr != nil {
			log.Warnf("referral: credit inviter %d: %v", inviterID, berr)
		}
	}
	// The signup is a confirmed invite: count it (for the invites leaderboard)
	// and check milestone tiers regardless of whether the cash reward is
	// immediate or deferred.
	s.DB.BumpInvites(ctx, inviterID)
	if active {
		s.checkMilestones(ctx, inviterID, camp)
	}
	log.Infof("referral: invite signup user=%d inviter=%d code=%s invitee+$%.2f inviter+$%.2f(paid=%d)",
		inviteeID, inviterID, card.Code, inviteeBonus, inviterBonus, inviterPaid)
	return inviteeBonus, camp.Name, true, false, err
}

// campaignFor resolves a card's campaign, falling back to the active one.
func (s *Service) campaignFor(ctx context.Context, campaignID int64) (*Campaign, error) {
	if campaignID > 0 {
		if c, err := s.GetCampaign(ctx, campaignID); err == nil {
			return c, nil
		}
	}
	return s.ActiveCampaign(ctx)
}

// insertConversion records one referral conversion. The invitee_user_id UNIQUE
// constraint makes it idempotent: a duplicate insert reports firstTime=false and
// the caller skips all crediting.
func (s *Service) insertConversion(ctx context.Context, campaignID int64, code string, inviterID, inviteeID int64, inviteeBonus, inviterBonus float64, fraudInt, inviterPaid int) (firstTime bool, err error) {
	res, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO referral_conversions
		(campaign_id, code, inviter_user_id, invitee_user_id, inviter_bonus_usd, invitee_bonus_usd, fraud, inviter_paid, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, strftime('%s','now'))`,
		campaignID, code, inviterID, inviteeID, inviterBonus, inviteeBonus, fraudInt, inviterPaid)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// countConfirmedInvites counts a user's non-fraud conversions (the cap basis +
// milestone basis).
func (s *Service) countConfirmedInvites(ctx context.Context, inviterID int64) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM referral_conversions WHERE inviter_user_id = ? AND fraud = 0`, inviterID).Scan(&n)
	return n
}

// checkMilestones grants any newly-reached tier bonuses. INSERT OR IGNORE on the
// (user_id, threshold) primary key guarantees each tier pays out at most once.
func (s *Service) checkMilestones(ctx context.Context, inviterID int64, camp *Campaign) {
	count := s.countConfirmedInvites(ctx, inviterID)
	tiers, err := s.ListTiers(ctx, camp.ID)
	if err != nil {
		return
	}
	for _, t := range tiers {
		if t.Threshold > count {
			continue
		}
		// Defer a paid tier when the daily budget is exhausted: skip entirely so
		// it isn't recorded-as-granted-but-unpaid, and is retried once budget frees.
		if t.BonusUSD > 0 && s.capTripped(ctx, camp.DailyBudgetUSD) {
			log.Warnf("referral: daily bonus budget cap reached — deferring tier %s for user %d", t.TierName, inviterID)
			continue
		}
		res, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO referral_milestone_grants
			(user_id, threshold, tier_id, bonus_usd, granted_at)
			VALUES (?, ?, ?, ?, strftime('%s','now'))`, inviterID, t.Threshold, t.ID, t.BonusUSD)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // already granted
		}
		if t.BonusUSD > 0 {
			note := fmt.Sprintf("邀请里程碑奖励: %s", t.TierName)
			if _, berr := s.DB.AddBalance(ctx, inviterID, txKindBonus, t.BonusUSD, "ref_tier:"+t.TierName, note, true); berr != nil {
				log.Warnf("referral: tier bonus user=%d tier=%s: %v", inviterID, t.TierName, berr)
			} else {
				log.Infof("referral: user %d reached tier %s (+$%.2f)", inviterID, t.TierName, t.BonusUSD)
			}
		}
	}
}

// ReleaseInviterReward pays out a deferred (reward_on=first_spend) inviter bonus
// when the invitee first spends. Fire-and-forget from the billing adapter; a
// single indexed lookup on the invitee, a no-op for the common signup-reward
// case. Idempotent via the guarded UPDATE.
func (s *Service) ReleaseInviterReward(ctx context.Context, inviteeID int64) {
	if s == nil {
		return
	}
	var inviterID int64
	var code string
	var bonus float64
	err := s.DB.QueryRowContext(ctx,
		`SELECT inviter_user_id, code, inviter_bonus_usd FROM referral_conversions WHERE invitee_user_id = ? AND inviter_paid = 0 AND fraud = 0`,
		inviteeID).Scan(&inviterID, &code, &bonus)
	if err != nil {
		return // no pending reward (or no row) — nothing to do
	}
	// Honour the daily budget cap: leave the reward pending (inviter_paid=0) so a
	// later charge retries it once the budget frees, instead of marking it paid
	// for $0.
	if bonus > 0 {
		var budgetCap float64
		if camp, cerr := s.ActiveCampaign(ctx); cerr == nil {
			budgetCap = camp.DailyBudgetUSD
		}
		if s.capTripped(ctx, budgetCap) {
			log.Warnf("referral: daily bonus budget cap reached — deferring inviter reward for invitee=%d", inviteeID)
			return
		}
	}
	res, uerr := s.DB.ExecContext(ctx, `UPDATE referral_conversions SET inviter_paid = 1 WHERE invitee_user_id = ? AND inviter_paid = 0`, inviteeID)
	if uerr != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // already released by a concurrent charge
	}
	if bonus > 0 {
		if _, berr := s.DB.AddBalance(ctx, inviterID, txKindBonus, bonus, "ref_inviter:"+code, "邀请好友首次消费奖励", true); berr != nil {
			log.Warnf("referral: release inviter reward user=%d: %v", inviterID, berr)
			return
		}
		log.Infof("referral: released deferred inviter reward user=%d (+$%.2f) after invitee=%d spent", inviterID, bonus, inviteeID)
	}
}
