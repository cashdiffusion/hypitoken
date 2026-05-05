package server

import (
	"context"

	"github.com/wjsoj/CPA-Claude/internal/usage"
)

// SaaSAdapter bridges the proxy to the optional multi-tenant SaaS layer.
// nil-safe: when Server.saas is nil, all calls degrade to the legacy
// clienttoken.Store flow. The implementation lives in internal/saas and is
// wired in main.go only when saas.enabled is true in config.
type SaaSAdapter interface {
	// Lookup resolves a Bearer token to a SaaS user. ok=false means this
	// token is not a SaaS token; the caller should fall back to the legacy
	// clienttoken store.
	Lookup(token string) (info SaaSTokenInfo, ok bool)

	// PreCheck enforces balance / per-token caps before forwarding. Returns
	// a non-nil PreCheckError (with HTTP status + JSON body) when the
	// request must be rejected.
	PreCheck(ctx context.Context, info SaaSTokenInfo) *PreCheckError

	// Charge deducts the bill from the user's wallet after a successful
	// upstream response. costUSD is the official cost from pricing.Catalog;
	// the SaaS layer applies the per-group multiplier internally and returns
	// the actual wallet USD that was charged.
	Charge(ctx context.Context, info SaaSTokenInfo, provider, model string, counts usage.Counts, officialCostUSD float64) (chargedUSD float64, err error)

	// CredentialGroup returns the upstream credential group the user's
	// pricing group is tied to (forwarded to auth.Pool.Acquire). Empty
	// string = public group.
	CredentialGroup(info SaaSTokenInfo) string

	// DefaultBillingRate returns the provider-level default billing rate
	// (1r = X USD). Used when a credential has no per-credential rate set.
	DefaultBillingRate(provider string) float64
}

// SaaSTokenInfo is the resolved identity for a SaaS Bearer token.
type SaaSTokenInfo struct {
	TokenID       int64
	UserID        int64
	Email         string
	Name          string  // token's friendly name
	GroupID       int64
	BalanceUSD    float64
	MaxConcurrent int
	RPM           int
	DailyUSDCap   float64
	MonthlyUSDCap float64
	Disabled      bool
}

// PreCheckError is the per-request rejection produced by SaaSAdapter.PreCheck.
type PreCheckError struct {
	Status int
	Body   any
}
