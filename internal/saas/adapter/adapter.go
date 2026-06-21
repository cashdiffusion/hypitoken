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
	return server.SaaSTokenInfo{
		TokenID:       t.ID,
		UserID:        u.ID,
		Email:         u.Email,
		Name:          t.Name,
		GroupID:       u.GroupID,
		BalanceUSD:    u.BalanceUSD,
		MaxConcurrent: t.MaxConcurrent,
		RPM:           t.RPM,
		DailyUSDCap:   t.DailyUSDCap,
		MonthlyUSDCap: t.MonthlyUSDCap,
		Disabled:      t.Disabled || u.Disabled,
		Groups:        append([]string(nil), t.Groups...),
	}, true
}

func (a *Adapter) PreCheck(ctx context.Context, info server.SaaSTokenInfo) *server.PreCheckError {
	if info.Disabled {
		return &server.PreCheckError{Status: http.StatusForbidden, Body: map[string]any{"error": "token or account disabled"}}
	}
	if info.BalanceUSD <= 0 {
		return &server.PreCheckError{Status: http.StatusPaymentRequired, Body: map[string]any{"error": "insufficient balance", "balance_usd": info.BalanceUSD}}
	}
	// Daily / monthly caps (enforced against wallet_tx ledger).
	now := time.Now()
	if info.DailyUSDCap > 0 {
		spent, err := a.DB.SumChargeSince(ctx, info.UserID, now.Truncate(24*time.Hour))
		if err == nil && spent >= info.DailyUSDCap {
			return &server.PreCheckError{Status: http.StatusTooManyRequests, Body: map[string]any{"error": "daily cap exceeded", "spent_usd": spent, "cap_usd": info.DailyUSDCap}}
		}
	}
	if info.MonthlyUSDCap > 0 {
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		spent, err := a.DB.SumChargeSince(ctx, info.UserID, monthStart)
		if err == nil && spent >= info.MonthlyUSDCap {
			return &server.PreCheckError{Status: http.StatusTooManyRequests, Body: map[string]any{"error": "monthly cap exceeded", "spent_usd": spent, "cap_usd": info.MonthlyUSDCap}}
		}
	}
	return nil
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
	// Deduct with an overdraft floor: a request that already hit upstream must
	// be billed, but the wallet can never be driven below -MaxOverdraftUSD by a
	// single huge request or a burst of concurrent ones. The clamped amount is
	// what we record, so the request log and the wallet ledger stay in lockstep.
	_, charged, err := a.DB.ChargeWithFloor(ctx, info.UserID, db.TxKindCharge, billed, ref, "", a.MaxOverdraftUSD)
	if err != nil {
		return 0, err
	}
	if charged < billed {
		log.Warnf("saas: overdraft floor hit for user %d — billed %.6f clamped to %.6f (max_overdraft=$%.2f)", info.UserID, billed, charged, a.MaxOverdraftUSD)
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
