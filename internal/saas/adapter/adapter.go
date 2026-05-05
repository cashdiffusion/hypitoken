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

	"github.com/wjsoj/CPA-Claude/internal/pricing"
	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/server"
	"github.com/wjsoj/CPA-Claude/internal/usage"
)

// Adapter implements server.SaaSAdapter against the SaaS DB. It is created
// in main.go and passed into server.New via WithSaaS.
type Adapter struct {
	DB      *db.DB
	Catalog *pricing.Catalog
	Rate    *billing.Rate

	// groupCache memoizes pricing groups for the request hot path. Groups
	// change rarely (admin action) so a 30s TTL is plenty.
	groupMu    sync.RWMutex
	groups     map[int64]*db.PricingGroup
	groupsAt   time.Time
	groupsTTL  time.Duration
}

func NewAdapter(store *db.DB, catalog *pricing.Catalog, rate *billing.Rate) *Adapter {
	return &Adapter{DB: store, Catalog: catalog, Rate: rate, groups: map[int64]*db.PricingGroup{}, groupsTTL: 30 * time.Second}
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

func (a *Adapter) Charge(ctx context.Context, info server.SaaSTokenInfo, provider, model string, counts usage.Counts, officialCostUSD float64) (float64, error) {
	g := a.group(ctx, info.GroupID)
	live := a.Rate.CNYPerUSD()
	bill := billing.Cost(a.Catalog, provider, model, counts, g, live)
	if bill <= 0 {
		return 0, nil
	}
	ref := fmt.Sprintf("token=%d model=%s", info.TokenID, model)
	if _, err := a.DB.AddBalance(ctx, info.UserID, db.TxKindCharge, -bill, ref, "", true); err != nil {
		return 0, err
	}
	a.DB.TouchUserToken(ctx, info.TokenID)
	return bill, nil
}

func (a *Adapter) CredentialGroup(info server.SaaSTokenInfo) string {
	g := a.group(context.Background(), info.GroupID)
	if g == nil {
		return ""
	}
	return g.CredentialGroup
}
