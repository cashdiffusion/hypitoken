package shop

import (
	"context"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v82"

	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
)

// chanMailer records the addresses it was asked to deliver to, so tests can
// assert (or not) that a delivery email fired without touching SMTP.
type chanMailer struct{ sent chan string }

func (m *chanMailer) Send(to, _ /*subject*/, _ /*html*/, _ /*text*/ string) error {
	select {
	case m.sent <- to:
	default:
	}
	return nil
}

func newTestShop(t *testing.T, db *DB, mailer *chanMailer) *Shop {
	t.Helper()
	gw, err := billing.NewStripeGateway(billing.StripeParams{ //nolint:gosec // G101: sk_test_dummy is not a real key; applyStripeSession makes no API call
		SecretKey: "sk_test_dummy",
		Currency:  "cny",
	})
	if err != nil {
		t.Fatalf("stripe gw: %v", err)
	}
	s, err := New(Config{SiteName: "Test"}, db, gw, mailer, "admintoken")
	if err != nil {
		t.Fatalf("shop new: %v", err)
	}
	// Settling an order spawns a delivery-email goroutine that ends in an
	// email_sent write. Cleanups run LIFO and openTestDB registered db.Close
	// first, so joining here lands before the close — without it the write
	// races the close and leaves a -wal file behind, failing t.TempDir's
	// removal in whichever test happens to lose.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.WaitDeliveries(ctx); err != nil {
			t.Errorf("delivery goroutines still running at test end: %v", err)
		}
	})
	return s
}

// paidSession builds a Checkout Session as Stripe would deliver it on a
// successful payment of the given CNY amount.
func paidSession(id string, amountCNY float64) *stripe.CheckoutSession {
	return &stripe.CheckoutSession{
		ID:            id,
		PaymentStatus: stripe.CheckoutSessionPaymentStatusPaid,
		Currency:      stripe.CurrencyCNY,
		AmountTotal:   int64(amountCNY * 100),
		Metadata:      map[string]string{"out_trade_no": ""},
	}
}

func seedPendingCardOrder(t *testing.T, db *DB, out string, amount float64, cards ...string) {
	t.Helper()
	ctx := context.Background()
	p := &Product{Name: "P", PriceCNY: amount, DeliveryType: DeliveryCard, Active: true}
	if err := db.CreateProduct(ctx, p); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if len(cards) > 0 {
		if _, err := db.AppendCardSecrets(ctx, p.ID, cards); err != nil {
			t.Fatalf("append cards: %v", err)
		}
	}
	o := &Order{OutTradeNo: out, ProductID: p.ID, ProductName: p.Name, Email: "a@b.c", QueryPassHash: "x", AmountCNY: amount, PaySessionID: "cs_" + out}
	if err := db.CreateOrder(ctx, o); err != nil {
		t.Fatalf("create order: %v", err)
	}
}

// TestApplyStripeSessionPaid — a paid session settles the order, pops a card,
// and triggers the delivery email.
func TestApplyStripeSessionPaid(t *testing.T) {
	db := openTestDB(t)
	mailer := &chanMailer{sent: make(chan string, 1)}
	s := newTestShop(t, db, mailer)
	ctx := context.Background()

	seedPendingCardOrder(t, db, "PAID1", 10, "CARD-A", "CARD-B")
	o, _ := db.GetOrder(ctx, "PAID1")

	if err := s.applyStripeSession(ctx, o, paidSession("cs_PAID1", 10), "test"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, _ := db.GetOrder(ctx, "PAID1")
	if got.Status != OrderPaid {
		t.Fatalf("status = %q, want paid", got.Status)
	}
	if got.Fulfillment != "CARD-A" {
		t.Fatalf("fulfilment = %q, want CARD-A", got.Fulfillment)
	}
	select {
	case <-mailer.sent:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery email not dispatched")
	}
}

// usdPaidSession builds a paid Checkout Session for a USD-priced product.
// With Adaptive Pricing the session currency + amount_total stay in USD even
// though the buyer paid a converted local amount.
func usdPaidSession(id string, usd float64) *stripe.CheckoutSession {
	s := paidSession(id, usd)
	s.Currency = stripe.CurrencyUSD
	return s
}

// TestApplyStripeSessionUSD — a USD-priced order settles against a USD session.
func TestApplyStripeSessionUSD(t *testing.T) {
	db := openTestDB(t)
	mailer := &chanMailer{sent: make(chan string, 1)}
	s := newTestShop(t, db, mailer)
	ctx := context.Background()

	p := &Product{Name: "USD", PriceCNY: 5, Currency: CurrencyUSD, DeliveryType: DeliveryCard, Active: true}
	if err := db.CreateProduct(ctx, p); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := db.AppendCardSecrets(ctx, p.ID, []string{"USD-CARD"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	o := &Order{OutTradeNo: "USD1", ProductID: p.ID, ProductName: p.Name, Email: "a@b.c", QueryPassHash: "x", AmountCNY: 5, Currency: CurrencyUSD, PaySessionID: "cs_USD1"}
	if err := db.CreateOrder(ctx, o); err != nil {
		t.Fatalf("create order: %v", err)
	}
	got, _ := db.GetOrder(ctx, "USD1")
	if got.Currency != CurrencyUSD {
		t.Fatalf("order currency = %q, want usd", got.Currency)
	}

	if err := s.applyStripeSession(ctx, got, usdPaidSession("cs_USD1", 5), "test"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	settled, _ := db.GetOrder(ctx, "USD1")
	if settled.Status != OrderPaid || settled.Fulfillment != "USD-CARD" {
		t.Fatalf("USD order not settled: status=%q fulfilment=%q", settled.Status, settled.Fulfillment)
	}
}

// TestApplyStripeSessionCurrencyMismatch — a USD order must NOT settle against
// a CNY session (currency tampering / wrong session); fulfilment is refused.
func TestApplyStripeSessionCurrencyMismatch(t *testing.T) {
	db := openTestDB(t)
	s := newTestShop(t, db, &chanMailer{sent: make(chan string, 1)})
	ctx := context.Background()

	p := &Product{Name: "USD", PriceCNY: 5, Currency: CurrencyUSD, DeliveryType: DeliveryCard, Active: true}
	_ = db.CreateProduct(ctx, p)
	_, _ = db.AppendCardSecrets(ctx, p.ID, []string{"USD-CARD"})
	o := &Order{OutTradeNo: "MIS1", ProductID: p.ID, ProductName: p.Name, Email: "a@b.c", QueryPassHash: "x", AmountCNY: 5, Currency: CurrencyUSD, PaySessionID: "cs_MIS1"}
	_ = db.CreateOrder(ctx, o)
	got, _ := db.GetOrder(ctx, "MIS1")

	// CNY session (amount 5) against a USD order → currency mismatch.
	if err := s.applyStripeSession(ctx, got, paidSession("cs_MIS1", 5), "test"); err != nil {
		t.Fatalf("apply (mismatch should be a swallowed no-op): %v", err)
	}
	settled, _ := db.GetOrder(ctx, "MIS1")
	if settled.Status != OrderPending {
		t.Fatalf("status = %q, want pending (currency mismatch must not fulfil)", settled.Status)
	}
}

// TestApplyStripeSessionUnpaidNoop — an async method still processing
// (payment_status != paid) must NOT fulfil; the order stays pending so the
// poll/webhook can settle it later.
func TestApplyStripeSessionUnpaidNoop(t *testing.T) {
	db := openTestDB(t)
	s := newTestShop(t, db, &chanMailer{sent: make(chan string, 1)})
	ctx := context.Background()

	seedPendingCardOrder(t, db, "UNP1", 10, "CARD-A")
	o, _ := db.GetOrder(ctx, "UNP1")

	sess := paidSession("cs_UNP1", 10)
	sess.PaymentStatus = stripe.CheckoutSessionPaymentStatusUnpaid
	if err := s.applyStripeSession(ctx, o, sess, "test"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, _ := db.GetOrder(ctx, "UNP1")
	if got.Status != OrderPending {
		t.Fatalf("status = %q, want pending (unpaid must not fulfil)", got.Status)
	}
	p, _ := db.GetProduct(ctx, got.ProductID)
	if p.StockAvailable != 1 {
		t.Fatalf("stock = %d, want 1 (no card consumed)", p.StockAvailable)
	}
}

// TestApplyStripeSessionAmountTamper — a paid session whose total is short of
// the order amount must be refused (no fulfilment, order stays pending).
func TestApplyStripeSessionAmountTamper(t *testing.T) {
	db := openTestDB(t)
	s := newTestShop(t, db, &chanMailer{sent: make(chan string, 1)})
	ctx := context.Background()

	seedPendingCardOrder(t, db, "TAM1", 100, "CARD-A")
	o, _ := db.GetOrder(ctx, "TAM1")

	// Session only paid ¥10 against a ¥100 order.
	if err := s.applyStripeSession(ctx, o, paidSession("cs_TAM1", 10), "test"); err != nil {
		t.Fatalf("apply (should swallow tamper as no-op): %v", err)
	}
	got, _ := db.GetOrder(ctx, "TAM1")
	if got.Status != OrderPending {
		t.Fatalf("status = %q, want pending (short payment must not fulfil)", got.Status)
	}
}

// TestApplyStripeSessionRescuesExpired — an async (Alipay) payment that
// confirms AFTER the expiry sweeper flipped the order to `expired` must still
// settle and deliver. Otherwise the buyer paid real money and never gets the
// card. Mirrors the SaaS pending-OR-expired credit guard.
func TestApplyStripeSessionRescuesExpired(t *testing.T) {
	db := openTestDB(t)
	mailer := &chanMailer{sent: make(chan string, 1)}
	s := newTestShop(t, db, mailer)
	ctx := context.Background()

	seedPendingCardOrder(t, db, "EXP1", 10, "CARD-A")
	// Simulate the sweeper: order aged out to expired before the late payment.
	if _, err := db.ExpirePendingBefore(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("expire: %v", err)
	}
	o, _ := db.GetOrder(ctx, "EXP1")
	if o.Status != OrderExpired {
		t.Fatalf("precondition: status = %q, want expired", o.Status)
	}

	if err := s.applyStripeSession(ctx, o, paidSession("cs_EXP1", 10), "test"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, _ := db.GetOrder(ctx, "EXP1")
	if got.Status != OrderPaid {
		t.Fatalf("status = %q, want paid (expired order must still settle on late payment)", got.Status)
	}
	if got.Fulfillment != "CARD-A" {
		t.Fatalf("fulfilment = %q, want CARD-A", got.Fulfillment)
	}
	select {
	case <-mailer.sent:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery email not dispatched for rescued order")
	}
}

// TestApplyStripeSessionIdempotent — webhook+poll racing on the same paid
// session must consume exactly one card.
func TestApplyStripeSessionIdempotent(t *testing.T) {
	db := openTestDB(t)
	s := newTestShop(t, db, &chanMailer{sent: make(chan string, 4)})
	ctx := context.Background()

	seedPendingCardOrder(t, db, "IDEM1", 10, "CARD-A", "CARD-B")

	for i := 0; i < 3; i++ {
		o, _ := db.GetOrder(ctx, "IDEM1")
		if err := s.applyStripeSession(ctx, o, paidSession("cs_IDEM1", 10), "test"); err != nil {
			t.Fatalf("apply #%d: %v", i, err)
		}
	}
	p, _ := db.GetProduct(ctx, 1)
	if p.StockAvailable != 1 {
		t.Fatalf("stock = %d, want 1 (exactly one card consumed across repeats)", p.StockAvailable)
	}
}
