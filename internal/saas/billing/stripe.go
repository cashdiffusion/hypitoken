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

// StripeGateway drives the embedded Payment Element top-up flow. Unlike the
// QR gateways (Alipay/Z-Pay) which implement the Gateway interface, Stripe has
// a different lifecycle — create a PaymentIntent, hand its client_secret to the
// browser, then confirm settlement via either a signed webhook OR a live
// PaymentIntent retrieval on poll. So it lives off to the side on the Handler
// (Handler.Stripe) rather than behind Gateway, and runs *alongside* whichever
// QR gateway is configured.
//
// Money is charged in Currency. With "usd" (default) it bills 1:1 — a $10
// top-up bills USD 10.00 and credits the wallet $10. With "cny" the handler
// converts the USD wallet credit to CNY via the live exchange rate before
// charging (Alipay can't present USD on a non-US Stripe account, so a
// CN-facing deploy charges CNY and still credits the USD wallet).
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

// minorUnits converts a major-unit amount (e.g. dollars / yuan) to the integer
// minor unit Stripe expects (cents / fen). Both currencies exercised on this
// surface — USD and CNY — are two-decimal, so a flat ×100 is correct.
func minorUnits(amount float64) int64 {
	return int64(math.Round(amount * 100))
}

// StripeIntent is the subset of a created PaymentIntent the topup handler
// returns to the browser.
type StripeIntent struct {
	PaymentIntentID string
	ClientSecret    string
}

// CreateTopUpIntent creates a PaymentIntent for a wallet top-up. chargeAmount is
// the amount billed in the gateway's presentment currency (USD 1:1, or the
// CNY-converted amount); usdCredit is the wallet credit, stamped into metadata
// for reconciliation. The order id and user id are stamped into metadata too so
// the webhook / poll path can map the settled intent back to our order without a
// side table.
func (g *StripeGateway) CreateTopUpIntent(ctx context.Context, outTradeNo string, chargeAmount, usdCredit float64, userID int64, description string) (*StripeIntent, error) {
	params := &stripe.PaymentIntentCreateParams{
		Amount:   stripe.Int64(minorUnits(chargeAmount)),
		Currency: stripe.String(g.currency),
		// Let the Payment Element surface every rail enabled in the dashboard
		// (or the pinned configuration). allow_redirects=always so Alipay /
		// WeChat Pay / crypto, which bounce through a hosted auth page, work.
		AutomaticPaymentMethods: &stripe.PaymentIntentCreateAutomaticPaymentMethodsParams{
			Enabled:        stripe.Bool(true),
			AllowRedirects: stripe.String(string(stripe.PaymentIntentAutomaticPaymentMethodsAllowRedirectsAlways)),
		},
		Description: stripe.String(description),
	}
	if g.pmcID != "" {
		params.PaymentMethodConfiguration = stripe.String(g.pmcID)
	}
	params.AddMetadata("out_trade_no", outTradeNo)
	params.AddMetadata("user_id", strconv.FormatInt(userID, 10))
	params.AddMetadata("usd_credit", strconv.FormatFloat(usdCredit, 'f', 2, 64))

	pi, err := g.sc.V1PaymentIntents.Create(ctx, params)
	if err != nil {
		return nil, err
	}
	return &StripeIntent{PaymentIntentID: pi.ID, ClientSecret: pi.ClientSecret}, nil
}

// RetrieveIntent fetches the live PaymentIntent — used by the poll path
// (orderStatus) and admin reconciliation to settle an order even when the
// webhook never arrived.
func (g *StripeGateway) RetrieveIntent(ctx context.Context, id string) (*stripe.PaymentIntent, error) {
	return g.sc.V1PaymentIntents.Retrieve(ctx, id, &stripe.PaymentIntentRetrieveParams{})
}

// ConstructEvent verifies a webhook payload's signature and returns the parsed
// event. Errors if no webhook secret is configured.
func (g *StripeGateway) ConstructEvent(payload []byte, sigHeader string) (stripe.Event, error) {
	if g.webhookSecret == "" {
		return stripe.Event{}, errors.New("stripe: webhook secret not configured")
	}
	return webhook.ConstructEvent(payload, sigHeader, g.webhookSecret)
}

// verifyIntentForOrder checks a retrieved/parsed PaymentIntent against the
// stored order before crediting: it must be settled, in our currency, and have
// collected at least the charged amount (in the presentment currency — USD for a
// 1:1 deploy, CNY for a rate-converted one).
func verifyIntentForOrder(pi *stripe.PaymentIntent, chargeAmount float64, wantCurrency string) error {
	if pi == nil {
		return errors.New("nil payment intent")
	}
	if pi.Status != stripe.PaymentIntentStatusSucceeded {
		return fmt.Errorf("payment intent %s not succeeded (status=%s)", pi.ID, pi.Status)
	}
	if wantCurrency != "" && !strings.EqualFold(string(pi.Currency), wantCurrency) {
		return fmt.Errorf("%w: currency mismatch (got=%s want=%s)", ErrOrderTampered, pi.Currency, wantCurrency)
	}
	want := minorUnits(chargeAmount)
	if pi.AmountReceived < want {
		return fmt.Errorf("%w: amount short (received=%d want=%d %s)", ErrOrderTampered, pi.AmountReceived, want, pi.Currency)
	}
	return nil
}
