package server

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/usage"
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

	// MultiplierFor returns the (workspace × provider) coefficient used to
	// scale official costs into wallet charges. The rate is a property of the
	// token's BILLING workspace (info), not a pricing group. Surfaced so the
	// request log can record the exact multiplier on each row.
	MultiplierFor(info SaaSTokenInfo, provider string) float64

	// CredentialGroup returns the upstream credential group the user's
	// pricing group is tied to (forwarded to auth.Pool.Acquire). Empty
	// string = public group.
	CredentialGroup(info SaaSTokenInfo) string
}

// SaaSTokenInfo is the resolved identity for a SaaS Bearer token.
type SaaSTokenInfo struct {
	TokenID       int64
	UserID        int64
	Email         string
	Name          string // token's friendly name
	GroupID       int64  // pricing group of the BILLING workspace (drives multiplier)
	BalanceUSD    float64
	MaxConcurrent int
	RPM           int
	DailyUSDCap   float64 // per-token daily cap (self-set)
	MonthlyUSDCap float64 // per-token monthly cap (self-set)
	Disabled      bool
	// Groups is the priority-ordered credential-group list set on the
	// token itself. When non-empty it overrides the user's pricing-group
	// credential mapping for dispatch (legacy single-group routing via
	// CredentialGroup still works as a fallback).
	Groups []string

	// Workspace billing subject (v13). The token charges WorkspaceID's pool.
	// BalanceUSD above is the workspace's balance. The caps stack — a request
	// is allowed only if it's under EVERY applicable cap:
	//   WorkspaceDailyCap / WorkspaceMonthlyCap — the shared pool's caps
	//   MemberMonthlyCap                        — this member's cap in the space
	//   min(MonthlyUSDCap, AdminMonthlyCap>0)   — the per-key cap (self + admin)
	WorkspaceID         int64
	WorkspaceDailyCap   float64
	WorkspaceMonthlyCap float64
	MemberMonthlyCap    float64
	AdminMonthlyCap     float64
	// Per-workspace billing multipliers (0 = standard default). The rate the
	// token's billing workspace charges; MultiplierFor picks claude vs codex.
	ClaudeMultiplier float64
	CodexMultiplier  float64
}

// PreCheckError is the per-request rejection produced by SaaSAdapter.PreCheck.
type PreCheckError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

// chargeCtx returns the context a wallet DEBIT must run under.
//
// The debit is the last step of a turn we have already paid the upstream for,
// so it must not be tied to the caller still being connected. Passing
// c.Request.Context() meant every client disconnect aborted the DB write and
// silently dropped the charge: production logged 14865 "charge failed: context
// canceled" over five days, spread across many distinct paying users, with no
// other error class present. Those turns cost us money upstream and billed
// nobody.
//
// WithoutCancel keeps the request's values (trace ids, deadlines set by
// middleware are dropped along with cancellation, which is what we want here)
// while detaching from its lifetime. The sibling fork already settles this way
// — see CPA-Claude's SettleCharge call sites — and this package already uses
// the same pattern for the referral reward in
// internal/saas/adapter/adapter.go.
//
// PreCheck deliberately does NOT use this: it is a read on the way in, it fails
// open, and if the caller is gone there is nothing left to authorize.
func chargeCtx(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return context.Background()
	}
	return context.WithoutCancel(c.Request.Context())
}
