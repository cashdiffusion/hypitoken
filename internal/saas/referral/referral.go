// Package referral implements hypitoken's viral growth system: personalised
// invite cards (platform-funded two-sided acquisition bonus) and peer gifting
// (wallet-to-wallet transfer with email escrow), plus the operator-configurable
// campaigns, milestone tiers, and analytics that wrap them.
//
// It is a self-contained SaaS module in the spirit of internal/saas/{growth,
// arena,analytics}: it owns its own tables (referral_campaigns / referral_tiers
// / referral_cards / referral_conversions / referral_milestone_grants /
// gift_cards from migration v11) and couples to the rest of the codebase only
// through the wallet ledger (db.DB), the email sender (Mailer), and the growth
// module (ChannelGranter — reused for signup anti-abuse and admin-channel
// fallback). Wiring happens in cmd/server/main.go.
//
// Two money flows, deliberately kept distinct:
//
//   - invite cards   — the platform credits BOTH the new user and the inviter a
//     configurable bonus on a fraud-clean registration. The inviter never pays.
//   - gift cards     — the sender's own balance is moved to the recipient via an
//     escrow row; unclaimed gifts expire back to the sender.
package referral

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// Mailer is the minimal slice of the mail package referral needs to deliver an
// invite / gift card. *mail.ResendMailer (and the SMTP / log mailers) satisfy it.
type Mailer interface {
	Send(to, subject, html, text string) error
}

// ChannelGranter is the growth-module seam: signup anti-abuse (shared
// signup_devices history) plus the admin marketing-channel signup-bonus path we
// fall back to when a ?ref= doesn't match a personal invite code.
// *growth.Service satisfies it. Kept an interface so referral neither imports
// growth nor double-records a device, and so it's stubbable in tests.
type ChannelGranter interface {
	RecordSignupDevice(ctx context.Context, userID int64, fp, ip, email string) (fraud bool, reason string, err error)
	GrantSignupBonus(ctx context.Context, userID int64, ref, vid, fp, ip, email string) (bonusUSD float64, channel string, matched, fraud bool, err error)
}

// Service is the referral module. Construct with New and hold one instance; it
// is safe for concurrent use (all state lives in SQLite).
type Service struct {
	DB       *db.DB
	Mailer   Mailer
	Channel  ChannelGranter
	SiteName string
	SiteURL  string

	// Suspended mirrors saas.referrals_enabled being off. The service is
	// still constructed and its admin routes still mounted when the
	// programme is suspended, because past grants have to stay auditable —
	// but the campaign and tier editors then have no effect until an
	// operator turns the programme back on and restarts. Surfacing it in
	// the analytics payload is what lets the admin UI say so, rather than
	// silently accepting edits that do nothing.
	Suspended bool

	claimRL *claimLimiter
}

// New builds the referral service. channel may be nil (signup anti-abuse + admin
// channel fallback then degrade to "clean, no channel"); mailer may be nil (the
// in-app card link still works, only email delivery is skipped).
func New(store *db.DB, mailer Mailer, channel ChannelGranter, siteName, siteURL string) *Service {
	return &Service{DB: store, Mailer: mailer, Channel: channel, SiteName: siteName, SiteURL: siteURL, claimRL: newClaimLimiter()}
}

// StartSweeper launches the background gift-expiry sweeper. It refunds every
// pending gift past its expiry back to the sender, once an hour, until ctx is
// cancelled (main.go passes the SaaS refresher context, cancelled on shutdown).
func (s *Service) StartSweeper(ctx context.Context) {
	if s == nil {
		return
	}
	go func() {
		// One sweep shortly after boot so a restart doesn't leave a long-expired
		// gift in limbo, then hourly.
		t := time.NewTimer(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sweepExpiredGifts(ctx)
				if s.claimRL != nil {
					s.claimRL.gc()
				}
				t.Reset(time.Hour)
			}
		}
	}()
}

// sweepExpiredGifts refunds every pending gift whose expiry has passed.
func (s *Service) sweepExpiredGifts(ctx context.Context) {
	gifts, err := s.DB.ListExpiredPendingGifts(ctx, time.Now())
	if err != nil {
		log.Warnf("referral: sweep expired gifts: %v", err)
		return
	}
	for _, g := range gifts {
		if _, err := s.DB.GiftRefundTx(ctx, g.Code); err != nil {
			log.Warnf("referral: refund expired gift %s: %v", g.Code, err)
			continue
		}
		log.Infof("referral: refunded expired gift %s ($%.2f) to user %d", g.Code, g.AmountUSD, g.SenderUserID)
	}
}
