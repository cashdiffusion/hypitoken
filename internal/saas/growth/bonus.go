package growth

import (
	"context"
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"
)

// GrantSignupBonus credits the channel's signup bonus to a freshly-registered
// user and records the conversion. It implements the referral seam the auth
// package calls right after CreateUser:
//
//	ref  — the ?ref= slug the browser captured at first touch (may be empty)
//	vid  — the anonymous visitor id, used to mark the originating visit converted
//
// Returns:
//
//	bonusUSD — the channel bonus actually credited (0 when none / fraud)
//	channel  — the matched channel's display name (for logging)
//	matched  — ref resolved to a real channel (even disabled/zero-bonus). When
//	           true the caller must skip the default trial credit; this channel
//	           owns the signup's bonus.
//	fraud    — the signup looks like abuse of the welcome bonus (same device
//	           fingerprint or shared subnet as prior users). When true NO bonus
//	           of any kind is paid — the caller must skip the trial credit too —
//	           and the frontend shows a "bonus withheld" notice.
//
// Always records a signup_devices row (the anti-abuse history) and, for a real
// channel, the conversion — even on fraud, so the channel's signup count stays
// accurate; it just pays out 0. A nil Service or an empty/unknown ref grant
// nothing and let the caller fall back to the trial credit. Errors are returned
// for the caller to log but must NOT block signup.
func (s *Service) GrantSignupBonus(ctx context.Context, userID int64, ref, vid, fp, ip, email string) (bonusUSD float64, channel string, matched, fraud bool, err error) {
	if s == nil {
		return 0, "", false, false, nil
	}
	// Anti-abuse first: record the device and decide whether this signup is a
	// suspected repeat. A device-record error is non-fatal — it's surfaced via
	// `err` but never blocks the signup, and on error we treat it as clean.
	fraud, _, err = s.RecordSignupDevice(ctx, userID, fp, ip, email)

	slug := NormalizeSlug(ref)
	if slug == "" {
		return 0, "", false, fraud, err
	}
	vid = sanitizeVisitorID(vid)

	ch, gerr := s.GetChannelBySlug(ctx, slug)
	if gerr != nil {
		if errors.Is(gerr, ErrNotFound) {
			// Unknown ref — nothing to credit, not an error worth surfacing.
			return 0, "", false, fraud, err
		}
		return 0, "", false, fraud, gerr
	}
	if !ch.Enabled {
		return 0, ch.Name, true, fraud, err
	}

	// Record the conversion regardless of bonus size / fraud so a $0, disabled,
	// or fraud-flagged channel still shows up in conversion analytics. On fraud
	// the bonus recorded is 0 and nothing is paid out. `firstTime` guards
	// against crediting the bonus twice if this ever runs more than once.
	recBonus := ch.BonusUSD
	if fraud {
		recBonus = 0
	}
	firstTime, rerr := s.RecordConversion(ctx, userID, slug, vid, recBonus)
	if rerr != nil {
		return 0, ch.Name, true, fraud, fmt.Errorf("record conversion: %w", rerr)
	}

	if fraud || !firstTime || ch.BonusUSD <= 0 {
		return 0, ch.Name, true, fraud, err
	}
	note := fmt.Sprintf("渠道注册赠额: %s", channelLabel(ch))
	if _, werr := s.wallet.AddBalance(ctx, userID, txKindBonus, ch.BonusUSD, "signup_bonus:"+slug, note, true); werr != nil {
		return 0, ch.Name, true, fraud, fmt.Errorf("credit bonus: %w", werr)
	}
	log.Infof("growth: granted $%.2f signup bonus to user %d via channel %q", ch.BonusUSD, userID, slug)
	return ch.BonusUSD, ch.Name, true, fraud, err
}

// channelLabel prefers the human name, falling back to the slug.
func channelLabel(ch *Channel) string {
	if ch.Name != "" {
		return ch.Name
	}
	return ch.Slug
}
