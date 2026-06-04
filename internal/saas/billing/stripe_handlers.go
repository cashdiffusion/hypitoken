package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	stripe "github.com/stripe/stripe-go/v82"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// providers reports which top-up rails are wired so the frontend can render
// the right surfaces (Stripe Payment Element vs the legacy QR dialog) and mount
// Stripe with the correct publishable key.
func (h *Handler) providers(c *gin.Context) {
	stripeCfg := gin.H{"enabled": false}
	if h.Stripe != nil {
		stripeCfg = gin.H{
			"enabled":         true,
			"publishable_key": h.Stripe.PublishableKey(),
			"currency":        h.Stripe.Currency(),
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"stripe": stripeCfg,
		// The QR rail (zpay/alipay/mock) is always present when billing is up.
		"qr": gin.H{"enabled": h.Gateway != nil},
	})
}

// topupStripe creates a Stripe Checkout Session for a wallet top-up and returns
// the client_secret + publishable key the browser needs to mount the
// CheckoutElementsProvider. The wallet is credited 1:1 in USD; Adaptive Pricing
// localizes the presentment currency for the buyer. The order is persisted
// pending; settlement happens later via the webhook or the orderStatus poll path.
func (h *Handler) topupStripe(c *gin.Context, userID int64, usd float64, out, email string) {
	if h.Stripe == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "stripe is not enabled"})
		return
	}
	subject := fmt.Sprintf("%s wallet top-up: $%.2f", h.Site, usd)
	returnURL := h.stripeReturnURL(out)
	sess, err := h.Stripe.CreateTopUpSession(c.Request.Context(), out, usd, userID, email, returnURL, subject)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "stripe: " + err.Error()})
		return
	}
	// Persist provider + Checkout Session id (NOT the client secret) in the
	// qr_code JSON column. cny_amount carries the USD with rate=1 so admin
	// revenue sums stay coherent across rails (the wallet credit is USD 1:1).
	stored, _ := json.Marshal(PayResult{Provider: "stripe", SessionID: sess.SessionID})
	if err := h.DB.CreateOrder(c.Request.Context(), db.AlipayOrder{
		OutTradeNo: out, UserID: userID, CNYAmount: round2(usd), USDCredit: usd,
		Rate: 1.0, QRCode: string(stored),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"out_trade_no":    out,
		"usd_credit":      usd,
		"cny_amount":      round2(usd),
		"rate":            1.0,
		"provider":        "stripe",
		"client_secret":   sess.ClientSecret,
		"publishable_key": h.Stripe.PublishableKey(),
		"session_id":      sess.SessionID,
		"currency":        h.Stripe.Currency(),
		"email":           email,
		"return_url":      returnURL,
	})
}

// stripeReturnURL is where redirect-based methods (Alipay/WeChat) send the
// browser back. We tack on our order id so the post-redirect billing page knows
// which order to resume polling for; Stripe appends its own session_id param.
func (h *Handler) stripeReturnURL(out string) string {
	base := strings.TrimRight(h.SiteURL, "/")
	if h.Stripe != nil && h.Stripe.ReturnURL() != "" {
		base = strings.TrimRight(h.Stripe.ReturnURL(), "/")
	} else if base != "" {
		base += "/app/billing"
	}
	if base == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "out=" + out
}

// maybeSettleStripeOrder is the poll-side settle hook: for a pending Stripe
// order it retrieves the live Checkout Session and credits if it has been paid.
// Best-effort and quiet — a not-yet-paid session is the normal case while the
// browser is mid-payment, so it returns without logging.
func (h *Handler) maybeSettleStripeOrder(ctx context.Context, o *db.AlipayOrder) {
	if h.Stripe == nil || o == nil || o.Status != db.OrderPending {
		return
	}
	pr := parsePayResult(o.QRCode)
	if pr.Provider != "stripe" || pr.SessionID == "" {
		return
	}
	sess, err := h.Stripe.RetrieveSession(ctx, pr.SessionID)
	if err != nil {
		log.Warnf("stripe poll: retrieve %s: %v", pr.SessionID, err)
		return
	}
	if err := h.applyStripeSession(ctx, o, sess, "stripe-poll"); err != nil {
		log.Warnf("stripe poll: apply order=%s: %v", o.OutTradeNo, err)
	}
}

// applyStripeSession credits a Stripe order from an authoritative Checkout
// Session (retrieved live or from a signature-verified webhook). A no-op when
// the session hasn't been paid yet (e.g. an async Alipay/WeChat payment still
// processing), so the poll path can call it repeatedly. Idempotent against
// webhook/poll races via CreditStripeOrder's status guard.
func (h *Handler) applyStripeSession(ctx context.Context, o *db.AlipayOrder, sess *stripe.CheckoutSession, source string) error {
	if o.Status == db.OrderPaid {
		return nil
	}
	if sess == nil {
		return errors.New("nil checkout session")
	}
	if sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		return nil // not settled yet — keep polling (async methods settle later)
	}
	if err := verifySessionForOrder(sess, o.USDCredit, h.Stripe.Currency()); err != nil {
		return err
	}
	note := fmt.Sprintf("stripe top-up $%.2f (%s)", o.USDCredit, source)
	if _, err := h.DB.CreditStripeOrder(ctx, o.OutTradeNo, sess.ID, o.UserID, o.USDCredit, o.OutTradeNo, note); err != nil {
		if errors.Is(err, db.ErrOrderNotPending) {
			return nil // already credited by a racing webhook/poll
		}
		return err
	}
	log.Infof("stripe [%s]: credited user=%d order=%s usd=%.2f session=%s",
		source, o.UserID, o.OutTradeNo, o.USDCredit, sess.ID)
	return nil
}

// stripeWebhook receives Stripe events. Signature is verified over the raw body.
// On checkout.session.completed (sync methods like cards) and
// checkout.session.async_payment_succeeded (redirect methods like Alipay/WeChat
// that settle asynchronously) it maps back to our order via metadata and credits
// the wallet.
func (h *Handler) stripeWebhook(c *gin.Context) {
	if h.Stripe == nil {
		c.String(http.StatusServiceUnavailable, "stripe not enabled")
		return
	}
	const maxBody = 1 << 20 // 1 MiB — Stripe payloads are small
	payload, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBody))
	if err != nil {
		c.String(http.StatusBadRequest, "read error")
		return
	}
	event, err := h.Stripe.ConstructEvent(payload, c.GetHeader("Stripe-Signature"))
	if err != nil {
		log.Warnf("stripe webhook: signature verify failed: %v", err)
		c.String(http.StatusBadRequest, "bad signature")
		return
	}

	switch event.Type {
	case stripe.EventTypeCheckoutSessionCompleted,
		stripe.EventTypeCheckoutSessionAsyncPaymentSucceeded:
		var sess stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
			c.String(http.StatusBadRequest, "bad payload")
			return
		}
		out := sess.Metadata["out_trade_no"]
		if out == "" {
			c.String(http.StatusOK, "ignored (no out_trade_no)")
			return
		}
		o, err := h.DB.GetOrder(c.Request.Context(), out)
		if err != nil {
			// Unknown order — ack so Stripe stops retrying; nothing to credit.
			log.Warnf("stripe webhook: order %s not found: %v", out, err)
			c.String(http.StatusOK, "order not found")
			return
		}
		if err := h.applyStripeSession(c.Request.Context(), o, &sess, "stripe-webhook"); err != nil {
			// Transient (DB) failure — 500 so Stripe retries. Validation
			// rejections are terminal but rare; logging covers them.
			log.Warnf("stripe webhook: apply order=%s: %v", out, err)
			c.String(http.StatusInternalServerError, "apply failed")
			return
		}
	default:
		// Other event types are acknowledged and ignored.
	}
	c.String(http.StatusOK, "ok")
}
