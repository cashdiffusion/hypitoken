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

// topupStripe creates a Stripe PaymentIntent for a wallet top-up and returns
// the client_secret + publishable key the browser needs to mount the Payment
// Element. The order is persisted pending; settlement happens later via the
// webhook or the orderStatus poll path. Charged 1:1 in the configured currency.
func (h *Handler) topupStripe(c *gin.Context, userID int64, usd float64, out string) {
	if h.Stripe == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "stripe is not enabled"})
		return
	}
	// Presentment amount + rate. USD bills the wallet credit 1:1; CNY converts
	// via the live exchange rate. Alipay on a non-US (e.g. Hong Kong) Stripe
	// account can't present USD, so the CN-facing deploy charges CNY and still
	// credits the USD wallet — exactly like the QR rail. cny_amount carries the
	// charged presentment amount so admin revenue sums stay coherent across rails.
	charge, rate := round2(usd), 1.0
	if h.Stripe.Currency() == "cny" {
		rate = h.Rate.CNYPerUSD()
		charge = round2(usd * rate)
	}
	subject := fmt.Sprintf("%s wallet top-up: $%.2f", h.Site, usd)
	intent, err := h.Stripe.CreateTopUpIntent(c.Request.Context(), out, charge, usd, userID, subject)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "stripe: " + err.Error()})
		return
	}
	// Persist provider + PaymentIntent id (NOT the client secret) in the
	// qr_code JSON column.
	stored, _ := json.Marshal(PayResult{Provider: "stripe", PaymentIntentID: intent.PaymentIntentID})
	if err := h.DB.CreateOrder(c.Request.Context(), db.AlipayOrder{
		OutTradeNo: out, UserID: userID, CNYAmount: charge, USDCredit: usd,
		Rate: rate, QRCode: string(stored),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"out_trade_no":      out,
		"usd_credit":        usd,
		"cny_amount":        charge,
		"rate":              rate,
		"provider":          "stripe",
		"client_secret":     intent.ClientSecret,
		"publishable_key":   h.Stripe.PublishableKey(),
		"payment_intent_id": intent.PaymentIntentID,
		"currency":          h.Stripe.Currency(),
		"return_url":        h.stripeReturnURL(),
	})
}

// stripeReturnURL is where redirect-based methods send the browser back.
func (h *Handler) stripeReturnURL() string {
	if h.Stripe != nil && h.Stripe.ReturnURL() != "" {
		return h.Stripe.ReturnURL()
	}
	base := strings.TrimRight(h.SiteURL, "/")
	if base == "" {
		return ""
	}
	return base + "/app/billing"
}

// maybeSettleStripeOrder is the poll-side settle hook: for a pending Stripe
// order it retrieves the live PaymentIntent and credits if it has succeeded.
// Best-effort and quiet — a not-yet-settled intent is the normal case while
// the browser is mid-payment, so it returns without logging.
func (h *Handler) maybeSettleStripeOrder(ctx context.Context, o *db.AlipayOrder) {
	if h.Stripe == nil || o == nil || o.Status != db.OrderPending {
		return
	}
	pr := parsePayResult(o.QRCode)
	if pr.Provider != "stripe" || pr.PaymentIntentID == "" {
		return
	}
	pi, err := h.Stripe.RetrieveIntent(ctx, pr.PaymentIntentID)
	if err != nil {
		log.Warnf("stripe poll: retrieve %s: %v", pr.PaymentIntentID, err)
		return
	}
	if err := h.applyStripeIntent(ctx, o, pi, "stripe-poll"); err != nil {
		log.Warnf("stripe poll: apply order=%s: %v", o.OutTradeNo, err)
	}
}

// applyStripeIntent credits a Stripe order from an authoritative PaymentIntent
// (retrieved live or from a signature-verified webhook). A no-op when the
// intent hasn't settled yet, so the poll path can call it repeatedly. Idempotent
// against webhook/poll races via CreditStripeOrder's status guard.
func (h *Handler) applyStripeIntent(ctx context.Context, o *db.AlipayOrder, pi *stripe.PaymentIntent, source string) error {
	if o.Status == db.OrderPaid {
		return nil
	}
	if pi == nil {
		return errors.New("nil payment intent")
	}
	if pi.Status != stripe.PaymentIntentStatusSucceeded {
		return nil // not settled yet — keep polling
	}
	// Verify against the charged presentment amount (cny_amount holds it for
	// both rails: USD 1:1 or the CNY-converted total), not the USD wallet credit.
	if err := verifyIntentForOrder(pi, o.CNYAmount, h.Stripe.Currency()); err != nil {
		return err
	}
	note := fmt.Sprintf("stripe top-up $%.2f (%s)", o.USDCredit, source)
	if _, err := h.DB.CreditStripeOrder(ctx, o.OutTradeNo, pi.ID, o.UserID, o.USDCredit, o.OutTradeNo, note); err != nil {
		if errors.Is(err, db.ErrOrderNotPending) {
			return nil // already credited by a racing webhook/poll
		}
		return err
	}
	log.Infof("stripe [%s]: credited user=%d order=%s usd=%.2f pi=%s",
		source, o.UserID, o.OutTradeNo, o.USDCredit, pi.ID)
	return nil
}

// stripeWebhook receives Stripe events. Signature is verified over the raw
// body. On payment_intent.succeeded it maps back to our order via metadata and
// credits the wallet.
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
	case stripe.EventTypePaymentIntentSucceeded:
		var pi stripe.PaymentIntent
		if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
			c.String(http.StatusBadRequest, "bad payload")
			return
		}
		out := pi.Metadata["out_trade_no"]
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
		if err := h.applyStripeIntent(c.Request.Context(), o, &pi, "stripe-webhook"); err != nil {
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
