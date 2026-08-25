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
// The session's single line item is priced in Currency (USD), the buyer is
// charged in USD, and the wallet is credited 1:1. Adaptive Pricing is off — see
// CreateTopUpSession for the measurements behind that. We moved off the raw
// PaymentIntents API when Adaptive Pricing was still in play (it only works with
// Checkout Sessions + Elements); Checkout Sessions stayed because the poll +
// webhook settlement paths are now built around them.
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
	// URL is the Stripe-hosted Checkout page (only set for hosted-mode
	// sessions created via CreateHostedCheckout; empty for custom-UI sessions).
	URL string
}

// CreateHostedCheckout creates a hosted-UI-mode Checkout Session and returns the
// session id + the Stripe-hosted page URL the buyer's browser should be
// redirected to. This is the right shape for a server-rendered storefront (no
// Stripe.js / Elements on our pages): Stripe owns the whole payment UI — card +
// Alipay tabs and the localized amount — then redirects back to successURL.
//
// The single line item is priced in g.currency (CNY for the shop, whose
// products are priced in CNY). Charging CNY directly makes Alipay natively
// eligible (its home currency) and cards work in any currency, so no Adaptive
// Pricing is needed here — unlike the USD top-up path. amount is the major-unit
// charge (e.g. 9.90 CNY). successURL should contain Stripe's
// {CHECKOUT_SESSION_ID} placeholder so the return page can poll-settle. extra
// metadata is merged into the session (e.g. out_trade_no) for webhook/poll
// mapping back to the order.
// currency is the presentment currency of the line item (e.g. "cny" or "usd").
// When adaptivePricing is true, Stripe localizes that currency to the buyer's
// locale at checkout — required for a USD-priced product to keep Alipay et al.
// eligible on a non-US account. CNY-priced products pass adaptivePricing=false
// (Alipay is natively eligible in its home currency, so no conversion is
// needed). Pass currency="" to fall back to the gateway's configured currency.
func (g *StripeGateway) CreateHostedCheckout(ctx context.Context, outTradeNo string, amount float64, currency string, adaptivePricing bool, email, successURL, cancelURL, description string, extra map[string]string) (*StripeSession, error) {
	cur := strings.ToLower(strings.TrimSpace(currency))
	if cur == "" {
		cur = g.currency
	}
	meta := map[string]string{"out_trade_no": outTradeNo}
	for k, v := range extra {
		meta[k] = v
	}
	params := &stripe.CheckoutSessionCreateParams{
		Mode:       stripe.String(string(stripe.CheckoutSessionModePayment)),
		SuccessURL: stripe.String(successURL),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{{
			Quantity: stripe.Int64(1),
			PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
				Currency:   stripe.String(cur),
				UnitAmount: stripe.Int64(minorUnits(amount)),
				ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{
					Name: stripe.String(description),
				},
			},
		}},
		Metadata: meta,
	}
	// Always set Adaptive Pricing explicitly (true for USD localization, false
	// for a direct CNY charge) so behaviour doesn't depend on the account's
	// default — some accounts default it ON, which would silently convert a
	// CNY-priced product for non-CNY buyers.
	params.AdaptivePricing = &stripe.CheckoutSessionCreateAdaptivePricingParams{
		Enabled: stripe.Bool(adaptivePricing),
	}
	if cancelURL != "" {
		params.CancelURL = stripe.String(cancelURL)
	}
	if email != "" {
		params.CustomerEmail = stripe.String(email)
	}
	if g.pmcID != "" {
		params.PaymentMethodConfiguration = stripe.String(g.pmcID)
	}
	sess, err := g.sc.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return nil, err
	}
	return &StripeSession{SessionID: sess.ID, ClientSecret: sess.ClientSecret, URL: sess.URL}, nil
}

// VerifyPaidSession is the exported guard used by callers outside this package
// (e.g. the shop): the session must be paid, in wantCurrency, and total at
// least amount (major units). With Adaptive Pricing the session currency +
// amount_total stay in the integration currency (what we priced in), even
// though the buyer paid a converted local amount — so wantCurrency is the
// product's pricing currency. Pass wantCurrency="" to skip the currency check.
func (g *StripeGateway) VerifyPaidSession(sess *stripe.CheckoutSession, amount float64, wantCurrency string) error {
	return verifySessionForOrder(sess, amount, strings.ToLower(strings.TrimSpace(wantCurrency)))
}

// CreateTopUpSession creates a custom-UI-mode Checkout Session for a wallet
// top-up: one USD line item, the customer's email prefilled, and the order id /
// user id / usd credit stamped into metadata so the webhook / poll path can map
// a settled session back to our order without a side table. returnURL is where
// redirect-based methods (Alipay/WeChat) send the browser back. Returns the
// session id + client_secret.
//
// Adaptive Pricing is deliberately OFF. It was originally enabled on the theory
// that Alipay needs a local presentment currency to be eligible on our
// Hong Kong account — it doesn't: 71 of the last 100 live charges settled as
// alipay/USD with no presentment conversion at all. What Adaptive Pricing did
// buy the buyer was a worse exchange rate. Measured against real settled
// sessions, its USD→CNY rate was ~6.99 ($10.00 → ¥69.90, $5.00 → ¥34.96,
// $20.00 → ¥139.85) while Alipay converting the same USD charge itself quotes
// ~6.76 — a ~3.4% spread, matching the 2–4% conversion fee Stripe documents as
// "you pay 0%, your customers pay 2–4%". Every basis point of that goes to
// Stripe: amount_total stays USD either way, so we credit the same wallet
// balance and receive the same settlement whichever currency the buyer picks.
// Charging USD outright hands the conversion back to Alipay's own bank rate.
//
// Keep this in lockstep with the client: Stripe requires the Currency Selector
// Element to be rendered whenever Adaptive Pricing is live on an Elements
// integration, so "leave it on but hide the picker" is not an option — the
// browser must drop adaptivePricing.allowed together with this flag.
func (g *StripeGateway) CreateTopUpSession(ctx context.Context, outTradeNo string, usd float64, userID int64, email, returnURL, description string) (*StripeSession, error) {
	params := &stripe.CheckoutSessionCreateParams{
		Mode:   stripe.String(string(stripe.CheckoutSessionModePayment)),
		UIMode: stripe.String(string(stripe.CheckoutSessionUIModeCustom)),
		// Set explicitly rather than relying on the account default, which can
		// be flipped on from the Dashboard without touching this code.
		AdaptivePricing: &stripe.CheckoutSessionCreateAdaptivePricingParams{
			Enabled: stripe.Bool(false),
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
