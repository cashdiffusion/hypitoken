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
// Returns the bonus actually granted and the channel's display name. A nil
// Service, an empty/unknown/disabled ref, or a zero bonus are all non-errors —
// they simply grant nothing, so registration never fails because of growth.
// Errors are returned for the caller to log but should NOT block signup.
func (s *Service) GrantSignupBonus(ctx context.Context, userID int64, ref, vid string) (bonusUSD float64, channel string, err error) {
	if s == nil {
		return 0, "", nil
	}
	slug := NormalizeSlug(ref)
	if slug == "" {
		return 0, "", nil
	}
	vid = sanitizeVisitorID(vid)

	ch, err := s.GetChannelBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Unknown ref — nothing to credit, not an error worth surfacing.
			return 0, "", nil
		}
		return 0, "", err
	}
	if !ch.Enabled {
		return 0, ch.Name, nil
	}

	// Record the conversion regardless of bonus size so a $0 channel still
	// shows up in conversion analytics. `firstTime` guards against crediting the
	// bonus twice if this ever runs more than once for a user.
	firstTime, rerr := s.RecordConversion(ctx, userID, slug, vid, ch.BonusUSD)
	if rerr != nil {
		return 0, ch.Name, fmt.Errorf("record conversion: %w", rerr)
	}

	if !firstTime || ch.BonusUSD <= 0 {
		return 0, ch.Name, nil
	}
	note := fmt.Sprintf("渠道注册赠额: %s", channelLabel(ch))
	if _, werr := s.wallet.AddBalance(ctx, userID, txKindBonus, ch.BonusUSD, "signup_bonus:"+slug, note, true); werr != nil {
		return 0, ch.Name, fmt.Errorf("credit bonus: %w", werr)
	}
	log.Infof("growth: granted $%.2f signup bonus to user %d via channel %q", ch.BonusUSD, userID, slug)
	return ch.BonusUSD, ch.Name, nil
}

// channelLabel prefers the human name, falling back to the slug.
func channelLabel(ch *Channel) string {
	if ch.Name != "" {
		return ch.Name
	}
	return ch.Slug
}
