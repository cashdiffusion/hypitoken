package main

import (
	"testing"

	"github.com/wjsoj/CPA-Claude/internal/saas"
)

func TestIsLocalSiteURL(t *testing.T) {
	cases := map[string]bool{
		"":                              false, // unset → fail closed
		"https://api.example.com":       false,
		"https://pay.novadiffusion.com": false,
		"http://localhost:5174":         true,
		"localhost":                     true,
		"http://127.0.0.1:8317":         true,
		"http://[::1]:8317":             true,
		"http://0.0.0.0:8317":           true,
		"http://dev.local":              true,
		"http://app.localhost":          true,
		"http://192.168.1.10":           false, // LAN, not loopback
	}
	for in, want := range cases {
		if got := isLocalSiteURL(in); got != want {
			t.Errorf("isLocalSiteURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSelectPaymentGatewayMockGuard(t *testing.T) {
	// No real gateway + public site → mock is refused.
	if _, _, err := selectPaymentGateway(saas.Config{SiteURL: "https://api.example.com"}); err == nil {
		t.Fatal("expected mock to be refused on a public site")
	}
	// Explicit payment_provider: mock on a public site is also refused.
	if _, _, err := selectPaymentGateway(saas.Config{PaymentProvider: "mock", SiteURL: "https://api.example.com"}); err == nil {
		t.Fatal("expected explicit mock to be refused on a public site")
	}
	// Local dev → mock is allowed.
	if _, name, err := selectPaymentGateway(saas.Config{SiteURL: "http://localhost:5174"}); err != nil {
		t.Fatalf("mock should be allowed on localhost: %v", err)
	} else if name == "" {
		t.Fatal("expected a gateway name")
	}
	// Explicit opt-in overrides the public-site guard.
	if _, _, err := selectPaymentGateway(saas.Config{SiteURL: "https://api.example.com", AllowMockPayment: true}); err != nil {
		t.Fatalf("allow_mock_payment should override the guard: %v", err)
	}
}
