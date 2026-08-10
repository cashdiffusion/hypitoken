package referral

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// Gift-flow validation errors (surfaced to the user verbatim by the handler).
var (
	ErrInvalidEmail   = errors.New("invalid recipient email")
	ErrSelfGift       = errors.New("cannot gift to yourself")
	ErrAmountTooSmall = errors.New("gift amount must be greater than zero")
	ErrAmountTooLarge = errors.New("gift amount exceeds the per-gift limit")
	ErrTopupRequired  = errors.New("add credit to your account before sending a gift")
)

func validEmail(email string) bool {
	return strings.Contains(email, "@") && strings.Contains(email, ".") && len(email) >= 5 && len(email) < 200
}

// roundCents rounds a USD amount to two decimals.
func roundCents(v float64) float64 {
	return math.Round(v*100) / 100
}

// SendGift transfers credit from the sender's wallet to a recipient identified
// by email. The sender is debited into escrow immediately; if the recipient is
// already a registered user the gift is claimed for them on the spot, otherwise
// it waits (and is auto-claimed when they register / expires back to the
// sender). Returns the resulting gift row.
func (s *Service) SendGift(ctx context.Context, senderID int64, senderEmail, recipientEmail string, amountUSD float64, message, style, tone string) (*db.GiftCard, error) {
	recipientEmail = strings.ToLower(strings.TrimSpace(recipientEmail))
	senderEmail = strings.ToLower(strings.TrimSpace(senderEmail))
	if !validEmail(recipientEmail) {
		return nil, ErrInvalidEmail
	}
	if recipientEmail == senderEmail {
		return nil, ErrSelfGift
	}
	amountUSD = roundCents(amountUSD)
	if amountUSD <= 0 {
		return nil, ErrAmountTooSmall
	}
	maxGift, expiryDays := 100.0, 30
	if camp, err := s.ActiveCampaign(ctx); err == nil {
		if camp.MaxGiftUSD > 0 {
			maxGift = camp.MaxGiftUSD
		}
		if camp.GiftExpiryDays > 0 {
			expiryDays = camp.GiftExpiryDays
		}
	}
	if amountUSD > maxGift {
		return nil, ErrAmountTooLarge
	}

	// A gift moves credit between accounts, so it is the one operation that lets
	// bonus money escape the account it was granted to. Require that the sender
	// has topped up at least once: paying customers gift freely, while an account
	// holding nothing but signup/referral bonuses cannot forward them on. This is
	// what the 2026-08-08 farm used — throwaway accounts gifted their $1 signup
	// bonus to the operator's main account seconds after registering.
	//
	// Checked here rather than in the handler so every caller is covered, and
	// placed after the cheap validation so a malformed request still gets the
	// more specific error.
	if paid, err := s.DB.HasEverToppedUp(ctx, senderID); err != nil {
		return nil, err
	} else if !paid {
		return nil, ErrTopupRequired
	}

	code, err := s.newRedeemCode(ctx)
	if err != nil {
		return nil, err
	}
	gift := db.GiftCard{
		SenderUserID:   senderID,
		Code:           code,
		RecipientEmail: recipientEmail,
		AmountUSD:      amountUSD,
		Message:        runeLimit(message, 140),
		CardStyle:      normalizeStyle(style),
		CardTone:       normalizeTone(tone),
		ExpiresAt:      time.Now().Add(time.Duration(expiryDays) * 24 * time.Hour),
	}
	if _, err := s.DB.GiftSendTx(ctx, gift); err != nil {
		return nil, err // ErrInsufficientBalance bubbles up
	}

	// If the recipient already has an account, claim it for them now so the
	// credit lands immediately; otherwise leave it pending for them to claim on
	// signup.
	claimedNow := false
	if recip, rerr := s.DB.GetUserByEmail(ctx, recipientEmail); rerr == nil && recip.ID != senderID && !recip.Disabled {
		if _, _, cerr := s.DB.GiftClaimTx(ctx, code, recip.ID); cerr == nil {
			claimedNow = true
		}
	}
	s.sendGiftEmail(recipientEmail, senderEmail, amountUSD, gift.Message, code, claimedNow)

	if final, ferr := s.DB.GetGiftByCode(ctx, code); ferr == nil {
		return final, nil
	}
	return &gift, nil
}

// ClaimGiftByCode claims a pending gift for the authenticated user by its redeem
// code. Possession of the code is the bearer right to claim, matching gift-card
// semantics. Returns the gift + the user's new balance.
func (s *Service) ClaimGiftByCode(ctx context.Context, rawCode string, userID int64) (*db.GiftCard, float64, error) {
	code := normalizeRedeemCode(rawCode)
	if code == "" {
		return nil, 0, db.ErrNotFound
	}
	return s.DB.GiftClaimTx(ctx, code, userID)
}

// AutoClaimForEmail claims every pending gift addressed to an email — called on
// registration and email verification so a gift sent before signup lands as soon
// as the recipient owns the inbox. Best-effort: errors are logged, never
// surfaced. Returns the number of gifts claimed and the total USD credited.
func (s *Service) AutoClaimForEmail(ctx context.Context, email string, userID int64) (int, float64) {
	if s == nil {
		return 0, 0
	}
	email = strings.ToLower(strings.TrimSpace(email))
	gifts, err := s.DB.ListPendingGiftsForEmail(ctx, email)
	if err != nil {
		log.Warnf("referral: auto-claim list for %s: %v", email, err)
		return 0, 0
	}
	var n int
	var total float64
	for _, g := range gifts {
		if g.SenderUserID == userID {
			continue // don't auto-claim your own gift back to yourself
		}
		if _, _, cerr := s.DB.GiftClaimTx(ctx, g.Code, userID); cerr != nil {
			if !errors.Is(cerr, db.ErrGiftNotClaimable) {
				log.Warnf("referral: auto-claim %s: %v", g.Code, cerr)
			}
			continue
		}
		n++
		total += g.AmountUSD
	}
	if n > 0 {
		log.Infof("referral: auto-claimed %d gift(s) ($%.2f) for user %d", n, total, userID)
	}
	return n, total
}

// sendGiftEmail delivers the gift card by email (best-effort). claimed=true when
// the recipient already had an account and the credit landed immediately.
func (s *Service) sendGiftEmail(to, from string, amount float64, message, code string, claimed bool) {
	if s.Mailer == nil {
		return
	}
	site := s.SiteName
	if site == "" {
		site = "HypiToken"
	}
	redeemURL := strings.TrimRight(s.SiteURL, "/") + "/app/invite?gift=" + code
	var subject, intro, action string
	if claimed {
		subject = fmt.Sprintf("%s 上有人给你充值了 $%.2f", site, amount)
		intro = fmt.Sprintf("%s 给你赠送了 $%.2f 余额，已自动到账。", from, amount)
		action = "登录查看余额"
	} else {
		subject = fmt.Sprintf("%s 上有人送了你一张 $%.2f 礼品卡", site, amount)
		intro = fmt.Sprintf("%s 给你送了一张 $%.2f 的 %s 礼品卡。注册并登录后即可领取。", from, amount, site)
		action = "注册并领取"
	}
	html := giftEmailHTML(site, intro, message, formatRedeem(code), redeemURL, action)
	text := giftEmailText(site, intro, message, formatRedeem(code), redeemURL)
	if err := s.Mailer.Send(to, subject, html, text); err != nil {
		log.Warnf("referral: gift email to %s: %v", to, err)
	}
}
