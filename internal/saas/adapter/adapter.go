// Package adapter implements server.SaaSAdapter and the /api/v2/* SaaS
// router. Lives in its own package so the leaf `saas` package (which holds
// Config) doesn't depend on server.
package adapter

import (
	"context"
	"fmt"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/saas/arena"
	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/server"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/usage"
)

// Default billing multipliers used when a pricing group has no value set
// (e.g. brand-new install before the operator has visited the admin panel).
// Match the seed values in db/migrations.go v3.
const (
	defaultClaudeMultiplier = 0.3
	defaultCodexMultiplier  = 0.05
)

// DefaultMaxOverdraftUSD bounds how far a wallet may be driven negative by
// in-flight requests when the operator hasn't set saas.max_overdraft_usd.
const DefaultMaxOverdraftUSD = 10.0

// Adapter implements server.SaaSAdapter against the SaaS DB. It is created
// in main.go and passed into server.New via WithSaaS.
type Adapter struct {
	DB      *db.DB
	Catalog *pricing.Catalog
	Rate    *billing.Rate

	// MaxOverdraftUSD caps how far a wallet may be driven negative by in-flight
	// requests. 0 disables the floor (unbounded negative). Set from
	// saas.max_overdraft_usd; NewAdapter seeds the default.
	MaxOverdraftUSD float64

	// Arena, when set, receives a per-request pulse for the public leaderboard
	// + real-time "Agent office". Optional / nil-safe — OnCharge is fire-and-
	// forget so it never blocks the billing hot path.
	Arena *arena.Service

	// Referral, when set, releases a deferred (reward_on=first_spend) inviter
	// bonus the first time an invited user actually spends. Optional / nil-safe;
	// invoked fire-and-forget off the request goroutine.
	Referral ReferralReleaser
}

// ReferralReleaser releases a deferred inviter reward when an invited user first
// spends. *saas/referral.Service satisfies it; kept an interface so the adapter
// doesn't import the referral package.
type ReferralReleaser interface {
	ReleaseInviterReward(ctx context.Context, inviteeUserID int64)
}

func NewAdapter(store *db.DB, catalog *pricing.Catalog, rate *billing.Rate) *Adapter {
	return &Adapter{DB: store, Catalog: catalog, Rate: rate, MaxOverdraftUSD: DefaultMaxOverdraftUSD}
}

func (a *Adapter) Lookup(token string) (server.SaaSTokenInfo, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	t, err := a.DB.GetUserTokenByValue(ctx, token)
	if err != nil {
		return server.SaaSTokenInfo{}, false
	}
	u, err := a.DB.GetUser(ctx, t.UserID)
	if err != nil {
		return server.SaaSTokenInfo{}, false
	}

	// Resolve the workspace this token bills (v13). Fall back to the user's
	// personal workspace for any legacy/unbound token.
	wsID := t.WorkspaceID
	if wsID == 0 {
		wsID, _ = a.DB.PersonalWorkspaceID(ctx, u.ID)
	}
	ws, err := a.DB.GetWorkspace(ctx, wsID)
	if err != nil {
		return server.SaaSTokenInfo{}, false
	}
	groupID := ws.GroupID
	if groupID == 0 {
		groupID = u.GroupID
	}
	// Membership in the billing workspace gates access + carries the per-member
	// cap. The owner of a personal workspace is always its admin member; an
	// enterprise member who has been removed must be denied.
	memberOK := false
	var memberCap float64
	if m, merr := a.DB.GetWorkspaceMember(ctx, wsID, u.ID); merr == nil {
		memberOK = true
		memberCap = m.MonthlyUSDCap
	}
	disabled := t.Disabled || u.Disabled || ws.Disabled
	if ws.Type == db.WorkspaceTypeEnterprise && !memberOK {
		disabled = true // removed from the enterprise space → token can't bill it
	}

	return server.SaaSTokenInfo{
		TokenID:             t.ID,
		UserID:              u.ID,
		Email:               u.Email,
		Name:                t.Name,
		GroupID:             groupID,
		BalanceUSD:          ws.BalanceUSD,
		MaxConcurrent:       t.MaxConcurrent,
		RPM:                 t.RPM,
		DailyUSDCap:         t.DailyUSDCap,
		MonthlyUSDCap:       t.MonthlyUSDCap,
		Disabled:            disabled,
		Groups:              append([]string(nil), t.Groups...),
		WorkspaceID:         wsID,
		WorkspaceDailyCap:   ws.DailyUSDCap,
		WorkspaceMonthlyCap: ws.MonthlyUSDCap,
		MemberMonthlyCap:    memberCap,
		AdminMonthlyCap:     t.AdminMonthlyCap,
		ClaudeMultiplier:    ws.ClaudeMultiplier,
		CodexMultiplier:     ws.CodexMultiplier,
	}, true
}

func (a *Adapter) PreCheck(ctx context.Context, info server.SaaSTokenInfo) *server.PreCheckError {
	if info.Disabled {
		return &server.PreCheckError{Status: http.StatusForbidden, Code: "account_disabled", Message: "This API key or account is disabled. Contact support if you believe this is a mistake."}
	}
	// Balance is the BILLING workspace's pool (personal or enterprise).
	if info.BalanceUSD <= 0 {
		return &server.PreCheckError{Status: http.StatusPaymentRequired, Code: "insufficient_balance", Message: "The account balance is insufficient. Add funds before retrying.", Details: map[string]any{"balance_usd": info.BalanceUSD}}
	}
	// Both cap windows open at local midnight, so "daily" and "monthly" mean
	// the same calendar to the customer reading them.
	//
	// dayStart used to be now.Truncate(24*time.Hour), which looks local but is
	// not: Truncate operates on absolute time since the zero instant, so it
	// always lands on UTC midnight whatever the server's zone. That was
	// invisible while the host ran UTC and became wrong the moment it moved to
	// Asia/Hong_Kong — a daily allowance resetting at 08:00 local, while the
	// monthly one (which really does follow the zone) reset at 00:00.
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	// Layer 1 — the shared workspace pool's own caps (no-op for personal spaces,
	// whose caps are 0). Enforced against the workspace-scoped ledger.
	if info.WorkspaceDailyCap > 0 {
		spent, err := a.DB.SumChargeSinceForWorkspace(ctx, info.WorkspaceID, dayStart)
		if err == nil && spent >= info.WorkspaceDailyCap {
			return capError("workspace_daily_limit_exceeded", "The workspace has reached its daily spending limit. Increase the limit or retry after the daily reset.", spent, info.WorkspaceDailyCap)
		}
	}
	if info.WorkspaceMonthlyCap > 0 {
		spent, err := a.DB.SumChargeSinceForWorkspace(ctx, info.WorkspaceID, monthStart)
		if err == nil && spent >= info.WorkspaceMonthlyCap {
			return capError("workspace_monthly_limit_exceeded", "The workspace has reached its monthly spending limit. Increase the limit or retry after the monthly reset.", spent, info.WorkspaceMonthlyCap)
		}
	}

	// Layer 2 — this member's cap within the workspace (prevents one member
	// draining the shared pool). Scoped to (workspace, user).
	if info.MemberMonthlyCap > 0 {
		spent, err := a.DB.SumChargeSinceForMember(ctx, info.WorkspaceID, info.UserID, monthStart)
		if err == nil && spent >= info.MemberMonthlyCap {
			return capError("member_monthly_limit_exceeded", "This workspace member has reached the monthly spending limit. Ask a workspace administrator to increase it or retry after the monthly reset.", spent, info.MemberMonthlyCap)
		}
	}

	// Layer 3 — the per-token caps (self-set; daily + monthly). These must use
	// token-scoped ledger totals: a sibling key's spend must not consume this
	// key's allowance. The space-admin per-key override (AdminMonthlyCap) is
	// folded into the effective monthly cap.
	if info.DailyUSDCap > 0 {
		spent, err := a.DB.SumChargeSinceForToken(ctx, info.UserID, info.TokenID, dayStart)
		if err == nil && spent >= info.DailyUSDCap {
			return capError("api_key_daily_limit_exceeded", "This API key has reached its daily spending limit. Increase the key limit or retry after the daily reset.", spent, info.DailyUSDCap)
		}
	}
	if mcap := effectiveMonthlyCap(info.MonthlyUSDCap, info.AdminMonthlyCap); mcap > 0 {
		spent, err := a.DB.SumChargeSinceForToken(ctx, info.UserID, info.TokenID, monthStart)
		if err == nil && spent >= mcap {
			return capError("api_key_monthly_limit_exceeded", "This API key has reached its monthly spending limit. Increase the key limit or retry after the monthly reset.", spent, mcap)
		}
	}
	return nil
}

func capError(code, message string, spent, limit float64) *server.PreCheckError {
	return &server.PreCheckError{Status: http.StatusTooManyRequests, Code: code, Message: message, Details: map[string]any{"spent_usd": spent, "limit_usd": limit}}
}

// effectiveMonthlyCap folds the owner's self-set per-token monthly cap with any
// space-admin-imposed cap, taking the tighter (smaller, non-zero) of the two.
// 0 on either side means "no cap from that source".
func effectiveMonthlyCap(self, admin float64) float64 {
	switch {
	case self <= 0:
		return admin
	case admin <= 0:
		return self
	case admin < self:
		return admin
	default:
		return self
	}
}

// Charge applies the user's group multiplier to the upstream-side official
// cost and deducts the result from their wallet. This is the single point
// where billing math happens — proxy paths just compute `official` via the
// pricing catalog and hand it off.
//
//	billed = official * multiplier(group, provider)
//
// Returns the billed amount so the caller can write it into the request log
// (so the log row matches the wallet ledger byte-for-byte).
func (a *Adapter) Charge(ctx context.Context, info server.SaaSTokenInfo, provider, model string, counts usage.Counts, officialCostUSD float64) (float64, error) {
	// Pulse the arena BEFORE the zero-cost early-return so cache-hit / free
	// requests still drive office activity + the leaderboard. Fire-and-forget.
	if a.Arena != nil {
		a.Arena.OnCharge(info.UserID, provider, model, totalTokens(counts))
	}
	if officialCostUSD <= 0 {
		return 0, nil
	}
	mult := a.MultiplierFor(info, provider)
	billed := billing.ChargeFromOfficial(officialCostUSD, mult)
	if billed <= 0 {
		return 0, nil
	}
	ref := fmt.Sprintf("token=%d model=%s", info.TokenID, model)
	// Deduct from the BILLING WORKSPACE's pool with an overdraft floor: a request
	// that already hit upstream must be billed, but the pool can never be driven
	// below -MaxOverdraftUSD by a single huge request or a burst of concurrent
	// ones. The clamped amount is what we record, so the request log and the
	// wallet ledger stay in lockstep. The charge is attributed to info.UserID
	// (the member who triggered it) for per-member usage/audit.
	_, charged, err := a.DB.ChargeWorkspaceWithFloor(ctx, info.WorkspaceID, info.UserID, db.TxKindCharge, billed, ref, "", a.MaxOverdraftUSD,
		db.ChargeMeta{
			TokenID:           info.TokenID,
			Model:             model,
			InputTokens:       counts.InputTokens,
			OutputTokens:      counts.OutputTokens,
			CacheReadTokens:   counts.CacheReadTokens,
			CacheCreateTokens: counts.CacheCreateTokens,
		})
	if err != nil {
		return 0, err
	}
	if charged < billed {
		log.Warnf("saas: overdraft floor hit for workspace %d (user %d) — billed %.6f clamped to %.6f (max_overdraft=$%.2f)", info.WorkspaceID, info.UserID, billed, charged, a.MaxOverdraftUSD)
	}
	billed = charged
	a.DB.TouchUserToken(ctx, info.TokenID)
	// First real spend by an invited user releases any deferred inviter reward.
	// Fire-and-forget off the billing goroutine; a no-op for the common case.
	// WithoutCancel keeps any request values but detaches from the request's
	// cancellation so the release survives the response returning.
	if a.Referral != nil && charged > 0 {
		go a.Referral.ReleaseInviterReward(context.WithoutCancel(ctx), info.UserID)
	}
	return billed, nil
}

// totalTokens sums all billable token axes for one request (input + output +
// both cache axes) — the activity weight shown on the leaderboard / office.
func totalTokens(c usage.Counts) int64 {
	return c.InputTokens + c.OutputTokens + c.CacheCreateTokens + c.CacheReadTokens
}

// MultiplierFor resolves the billing multiplier from the token's BILLING
// workspace (carried on info). 0 on the workspace means "standard default"
// (claude=0.3, codex=0.05) — personal workspaces always use the default; only
// enterprise workspaces carry a custom (discounted) rate.
func (a *Adapter) MultiplierFor(info server.SaaSTokenInfo, provider string) float64 {
	if auth.NormalizeProvider(provider) == auth.ProviderOpenAI {
		if info.CodexMultiplier > 0 {
			return info.CodexMultiplier
		}
		return defaultCodexMultiplier
	}
	if info.ClaudeMultiplier > 0 {
		return info.ClaudeMultiplier
	}
	return defaultClaudeMultiplier
}

// CredentialGroup is retained for the SaaSAdapter interface but no longer
// resolves via pricing groups (their credential_group mapping is unused —
// upstream routing runs off the per-token groups list). Always "" = public.
func (a *Adapter) CredentialGroup(_ server.SaaSTokenInfo) string { return "" }
