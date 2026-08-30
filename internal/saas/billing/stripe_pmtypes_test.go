package billing

import (
	"testing"

	stripe "github.com/stripe/stripe-go/v82"
)

// TestNormalizePaymentMethodTypes: order is load-bearing (it becomes the
// Payment Element's tab order, so entry 0 is the preselected rail), so
// normalization must clean the values without reshuffling them.
func TestNormalizePaymentMethodTypes(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil stays nil", nil, nil},
		{"blank-only collapses to nil", []string{"", "  "}, nil},
		{"trimmed and lowercased", []string{" Alipay ", "CARD"}, []string{"alipay", "card"}},
		{"order preserved", []string{"card", "alipay"}, []string{"card", "alipay"}},
		{"deduped, first wins", []string{"alipay", "card", "alipay"}, []string{"alipay", "card"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizePaymentMethodTypes(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestApplyPaymentMethodSelection: Stripe rejects a session carrying both
// payment_method_types and payment_method_configuration, so exactly one may be
// set — and an explicit rail list has to win, otherwise pinning Alipay would
// silently fall back to the dashboard's dynamic filter.
func TestApplyPaymentMethodSelection(t *testing.T) {
	t.Run("dynamic when neither configured", func(t *testing.T) {
		p := &stripe.CheckoutSessionCreateParams{}
		(&StripeGateway{}).applyPaymentMethodSelection(p)
		if p.PaymentMethodTypes != nil || p.PaymentMethodConfiguration != nil {
			t.Fatalf("expected an untouched params object, got %+v", p)
		}
	})
	t.Run("configuration used when no explicit list", func(t *testing.T) {
		p := &stripe.CheckoutSessionCreateParams{}
		(&StripeGateway{pmcID: "pmc_123"}).applyPaymentMethodSelection(p)
		if p.PaymentMethodTypes != nil {
			t.Errorf("unexpected payment_method_types: %v", p.PaymentMethodTypes)
		}
		if p.PaymentMethodConfiguration == nil || *p.PaymentMethodConfiguration != "pmc_123" {
			t.Errorf("payment_method_configuration not applied: %+v", p.PaymentMethodConfiguration)
		}
	})
	t.Run("explicit list wins over configuration", func(t *testing.T) {
		p := &stripe.CheckoutSessionCreateParams{}
		g := &StripeGateway{pmcID: "pmc_123", pmTypes: []string{"alipay", "card"}}
		g.applyPaymentMethodSelection(p)
		if p.PaymentMethodConfiguration != nil {
			t.Errorf("both fields set — Stripe would reject this session: %+v", p)
		}
		if len(p.PaymentMethodTypes) != 2 || *p.PaymentMethodTypes[0] != "alipay" || *p.PaymentMethodTypes[1] != "card" {
			t.Errorf("payment_method_types wrong: %v", p.PaymentMethodTypes)
		}
	})
}

// TestNewStripeGatewayNormalizesPaymentMethodTypes wires the config-shaped
// input through the constructor the way main.go does.
func TestNewStripeGatewayNormalizesPaymentMethodTypes(t *testing.T) {
	g, err := NewStripeGateway(StripeParams{ //nolint:gosec // G101: sk_test_dummy is not a real key; no API call is made
		SecretKey:          "sk_test_dummy",
		PaymentMethodTypes: []string{" Alipay", "card", "ALIPAY", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := g.PaymentMethodTypes()
	if len(got) != 2 || got[0] != "alipay" || got[1] != "card" {
		t.Fatalf("PaymentMethodTypes() = %v", got)
	}
}
