package billing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	stripe "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"
)

// StripeGateway drives the embedded Checkout-Sessions top-up flow. Unlike the
// QR gateways (Alipay/Z-Pay) which implement the Gateway interface, Stripe has
// a different lifecycle — create a Checkout Session, hand its client_secret to
// the browser's CheckoutElementsProvider, then confirm settlement via either a
// signed webhook OR a live session retrieval on poll. So it lives off to the
// side on the Handler (Handler.Stripe) rather than behind Gateway, and runs
// *alongside* whichever QR gateway is configured.
//
// The session's single line item is priced in Currency (USD) and the wallet is
// credited 1:1 in USD. Adaptive Pricing is enabled, so Stripe auto-converts the
// presentment currency to the buyer's local currency at checkout — which is what
// unlocks Alipay/WeChat/local rails on a non-US account. The buyer pays Stripe's
// 2-4% conversion fee on the converted leg; we receive the full USD amount. We
// moved off the raw PaymentIntents API because Adaptive Pricing is only
// supported with Checkout Sessions + Elements, not with PaymentIntents.
type StripeGateway struct {
	sc             *stripe.Client
	publishableKey string
	webhookSecret  string
	currency       string
	// pmcID optionally pins a payment_method_configuration (pmc_…). Empty =
	// rely on the account's default automatic payment methods.
	pmcID     string
	returnURL string
}

// StripeParams configures NewStripeGateway. Secret/webhook values must already
// be resolved (the @path indirection is handled by the caller).
type StripeParams struct {
	SecretKey                  string
	PublishableKey             string
	WebhookSecret              string
	Currency                   string
	PaymentMethodConfiguration string
	ReturnURL                  string
}

func NewStripeGateway(p StripeParams) (*StripeGateway, error) {
	secret := strings.TrimSpace(p.SecretKey)
	if secret == "" {
		return nil, errors.New("stripe: secret_key is required")
	}
	cur := strings.ToLower(strings.TrimSpace(p.Currency))
	if cur == "" {
		cur = "usd"
	}
	return &StripeGateway{
		sc:             stripe.NewClient(secret),
		publishableKey: strings.TrimSpace(p.PublishableKey),
		webhookSecret:  strings.TrimSpace(p.WebhookSecret),
		currency:       cur,
		pmcID:          strings.TrimSpace(p.PaymentMethodConfiguration),
		returnURL:      strings.TrimSpace(p.ReturnURL),
	}, nil
}

func (g *StripeGateway) PublishableKey() string { return g.publishableKey }
func (g *StripeGateway) Currency() string       { return g.currency }
func (g *StripeGateway) ReturnURL() string      { return g.returnURL }
func (g *StripeGateway) HasWebhookSecret() bool { return g.webhookSecret != "" }

// minorUnits converts a major-unit amount (e.g. dollars) to the integer minor
// unit Stripe expects (cents). The line item is always priced in USD (a
// two-decimal currency), so a flat ×100 is correct.
func minorUnits(amount float64) int64 {
	return int64(math.Round(amount * 100))
}

// StripeSession is the subset of a created Checkout Session the topup handler
// returns to the browser (client_secret) and persists (id).
type StripeSession struct {
	SessionID    string
	ClientSecret string
}

// CreateTopUpSession creates a custom-UI-mode Checkout Session for a wallet
// top-up: one USD line item, Adaptive Pricing on (Stripe localizes the
// presentment currency for the buyer, unlocking Alipay et al.), the customer's
// email prefilled, and the order id / user id / usd credit stamped into metadata
// so the webhook / poll path can map a settled session back to our order without
// a side table. returnURL is where redirect-based methods (Alipay/WeChat) send
// the browser back. Returns the session id + client_secret.
func (g *StripeGateway) CreateTopUpSession(ctx context.Context, outTradeNo string, usd float64, userID int64, email, returnURL, description string) (*StripeSession, error) {
	params := &stripe.CheckoutSessionCreateParams{
		Mode:   stripe.String(string(stripe.CheckoutSessionModePayment)),
		UIMode: stripe.String(string(stripe.CheckoutSessionUIModeCustom)),
		// Adaptive Pricing converts the USD price to the buyer's local currency,
		// which is what makes Alipay/WeChat eligible on a non-US account.
		AdaptivePricing: &stripe.CheckoutSessionCreateAdaptivePricingParams{
			Enabled: stripe.Bool(true),
		},
		ReturnURL: stripe.String(returnURL),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{{
			Quantity: stripe.Int64(1),
			PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
				Currency:   stripe.String(g.currency),
				UnitAmount: stripe.Int64(minorUnits(usd)),
				ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{
					Name: stripe.String(description),
				},
			},
		}},
		Metadata: map[string]string{
			"out_trade_no": outTradeNo,
			"user_id":      strconv.FormatInt(userID, 10),
			"usd_credit":   strconv.FormatFloat(usd, 'f', 2, 64),
		},
	}
	if email != "" {
		params.CustomerEmail = stripe.String(email)
	}

	sess, err := g.sc.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return nil, err
	}
	return &StripeSession{SessionID: sess.ID, ClientSecret: sess.ClientSecret}, nil
}

// RetrieveSession fetches the live Checkout Session — used by the poll path
// (orderStatus) and admin reconciliation to settle an order even when the
// webhook never arrived.
func (g *StripeGateway) RetrieveSession(ctx context.Context, id string) (*stripe.CheckoutSession, error) {
	return g.sc.V1CheckoutSessions.Retrieve(ctx, id, &stripe.CheckoutSessionRetrieveParams{})
}

// ConstructEvent verifies a webhook payload's signature and returns the parsed
// event. Errors if no webhook secret is configured.
//
// IgnoreAPIVersionMismatch: the Stripe account's default API version (what
// webhook payloads are serialized with) is newer than the version stripe-go is
// pinned to, and ConstructEvent rejects the signature on that mismatch by
// default. The fields we read (metadata, payment_status, amount_total, currency,
// id) are stable across versions, so we accept the newer payloads rather than
// dropping every event.
func (g *StripeGateway) ConstructEvent(payload []byte, sigHeader string) (stripe.Event, error) {
	if g.webhookSecret == "" {
		return stripe.Event{}, errors.New("stripe: webhook secret not configured")
	}
	return webhook.ConstructEventWithOptions(payload, sigHeader, g.webhookSecret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
}

// verifySessionForOrder checks a retrieved/parsed Checkout Session against the
// stored order before crediting: it must be paid, in our integration currency
// (USD — Adaptive Pricing keeps the session amount in our currency even when the
// buyer pays a converted local amount), and total at least the order's USD
// credit. amount_total is the authoritative USD figure regardless of the
// buyer's presentment currency.
func verifySessionForOrder(sess *stripe.CheckoutSession, usdCredit float64, wantCurrency string) error {
	if sess == nil {
		return errors.New("nil checkout session")
	}
	if sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		return fmt.Errorf("checkout session %s not paid (status=%s payment_status=%s)", sess.ID, sess.Status, sess.PaymentStatus)
	}
	if wantCurrency != "" && !strings.EqualFold(string(sess.Currency), wantCurrency) {
		return fmt.Errorf("%w: currency mismatch (got=%s want=%s)", ErrOrderTampered, sess.Currency, wantCurrency)
	}
	want := minorUnits(usdCredit)
	if sess.AmountTotal < want {
		return fmt.Errorf("%w: amount short (total=%d want=%d %s)", ErrOrderTampered, sess.AmountTotal, want, sess.Currency)
	}
	return nil
}
