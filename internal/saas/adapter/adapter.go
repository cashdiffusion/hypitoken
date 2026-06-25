// Package adapter implements server.SaaSAdapter and the /api/v2/* SaaS
// router. Lives in its own package so the leaf `saas` package (which holds
// Config) doesn't depend on server.
package adapter

import (
	"context"
	"fmt"
	"net/http"
	"sync"
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

	// groupCache memoizes pricing groups for the request hot path. Groups
	// change rarely (admin action) so a 30s TTL is plenty.
	groupMu   sync.RWMutex
	groups    map[int64]*db.PricingGroup
	groupsAt  time.Time
	groupsTTL time.Duration
}

// ReferralReleaser releases a deferred inviter reward when an invited user first
// spends. *saas/referral.Service satisfies it; kept an interface so the adapter
// doesn't import the referral package.
type ReferralReleaser interface {
	ReleaseInviterReward(ctx context.Context, inviteeUserID int64)
}

func NewAdapter(store *db.DB, catalog *pricing.Catalog, rate *billing.Rate) *Adapter {
	return &Adapter{DB: store, Catalog: catalog, Rate: rate, MaxOverdraftUSD: DefaultMaxOverdraftUSD, groups: map[int64]*db.PricingGroup{}, groupsTTL: 30 * time.Second}
}

func (a *Adapter) refreshGroups(ctx context.Context) {
	a.groupMu.RLock()
	fresh := time.Since(a.groupsAt) < a.groupsTTL && len(a.groups) > 0
	a.groupMu.RUnlock()
	if fresh {
		return
	}
	gs, err := a.DB.ListGroups(ctx)
	if err != nil {
		return
	}
	a.groupMu.Lock()
	a.groups = map[int64]*db.PricingGroup{}
	for _, g := range gs {
		a.groups[g.ID] = g
	}
	a.groupsAt = time.Now()
	a.groupMu.Unlock()
}

func (a *Adapter) group(ctx context.Context, id int64) *db.PricingGroup {
	a.refreshGroups(ctx)
	a.groupMu.RLock()
	defer a.groupMu.RUnlock()
	if g, ok := a.groups[id]; ok {
		return g
	}
	for _, g := range a.groups {
		if g.IsDefault {
			return g
		}
	}
	return nil
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
	}, true
}

func (a *Adapter) PreCheck(ctx context.Context, info server.SaaSTokenInfo) *server.PreCheckError {
	if info.Disabled {
		return &server.PreCheckError{Status: http.StatusForbidden, Body: map[string]any{"error": "token or account disabled"}}
	}
	// Balance is the BILLING workspace's pool (personal or enterprise).
	if info.BalanceUSD <= 0 {
		return &server.PreCheckError{Status: http.StatusPaymentRequired, Body: map[string]any{"error": "insufficient balance", "balance_usd": info.BalanceUSD}}
	}
	now := time.Now()
	dayStart := now.Truncate(24 * time.Hour)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	// Layer 1 — the shared workspace pool's own caps (no-op for personal spaces,
	// whose caps are 0). Enforced against the workspace-scoped ledger.
	if info.WorkspaceDailyCap > 0 {
		spent, err := a.DB.SumChargeSinceForWorkspace(ctx, info.WorkspaceID, dayStart)
		if err == nil && spent >= info.WorkspaceDailyCap {
			return &server.PreCheckError{Status: http.StatusTooManyRequests, Body: map[string]any{"error": "workspace daily cap exceeded", "spent_usd": spent, "cap_usd": info.WorkspaceDailyCap}}
		}
	}
	if info.WorkspaceMonthlyCap > 0 {
		spent, err := a.DB.SumChargeSinceForWorkspace(ctx, info.WorkspaceID, monthStart)
		if err == nil && spent >= info.WorkspaceMonthlyCap {
			return &server.PreCheckError{Status: http.StatusTooManyRequests, Body: map[string]any{"error": "workspace monthly cap exceeded", "spent_usd": spent, "cap_usd": info.WorkspaceMonthlyCap}}
		}
	}

	// Layer 2 — this member's cap within the workspace (prevents one member
	// draining the shared pool). Scoped to (workspace, user).
	if info.MemberMonthlyCap > 0 {
		spent, err := a.DB.SumChargeSinceForMember(ctx, info.WorkspaceID, info.UserID, monthStart)
		if err == nil && spent >= info.MemberMonthlyCap {
			return &server.PreCheckError{Status: http.StatusTooManyRequests, Body: map[string]any{"error": "member monthly cap exceeded", "spent_usd": spent, "cap_usd": info.MemberMonthlyCap}}
		}
	}

	// Layer 3 — the per-token caps (self-set; daily + monthly). Attributed to
	// the member's own spend. The space-admin per-key override (AdminMonthlyCap)
	// is folded into the effective monthly cap.
	if info.DailyUSDCap > 0 {
		spent, err := a.DB.SumChargeSince(ctx, info.UserID, dayStart)
		if err == nil && spent >= info.DailyUSDCap {
			return &server.PreCheckError{Status: http.StatusTooManyRequests, Body: map[string]any{"error": "daily cap exceeded", "spent_usd": spent, "cap_usd": info.DailyUSDCap}}
		}
	}
	if mcap := effectiveMonthlyCap(info.MonthlyUSDCap, info.AdminMonthlyCap); mcap > 0 {
		spent, err := a.DB.SumChargeSince(ctx, info.UserID, monthStart)
		if err == nil && spent >= mcap {
			return &server.PreCheckError{Status: http.StatusTooManyRequests, Body: map[string]any{"error": "monthly cap exceeded", "spent_usd": spent, "cap_usd": mcap}}
		}
	}
	return nil
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
	mult := a.MultiplierFor(ctx, info.GroupID, provider)
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
	_, charged, err := a.DB.ChargeWorkspaceWithFloor(ctx, info.WorkspaceID, info.UserID, db.TxKindCharge, billed, ref, "", a.MaxOverdraftUSD)
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

// MultiplierFor resolves the multiplier for (group, provider). Falls back
// to the package defaults (claude=0.3, codex=0.05) when the group is missing
// or its value is unset.
func (a *Adapter) MultiplierFor(ctx context.Context, groupID int64, provider string) float64 {
	if g := a.group(ctx, groupID); g != nil {
		switch auth.NormalizeProvider(provider) {
		case auth.ProviderOpenAI:
			if g.CodexMultiplier > 0 {
				return g.CodexMultiplier
			}
		default:
			if g.ClaudeMultiplier > 0 {
				return g.ClaudeMultiplier
			}
		}
	}
	if auth.NormalizeProvider(provider) == auth.ProviderOpenAI {
		return defaultCodexMultiplier
	}
	return defaultClaudeMultiplier
}

func (a *Adapter) CredentialGroup(info server.SaaSTokenInfo) string {
	g := a.group(context.Background(), info.GroupID)
	if g == nil {
		return ""
	}
	return g.CredentialGroup
}
