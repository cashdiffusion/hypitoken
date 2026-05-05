package billing

import (
	"strings"

	"github.com/wjsoj/CPA-Claude/internal/pricing"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/usage"
)

// Cost computes the wallet USD that should be charged for a given request.
//
//   bill = official × (provider_rate_rmb_per_usd / live_cny_per_usd) × group_multiplier
//
// The wallet is real USD: a request that officially costs $0.10 is charged
// e.g. $0.0278 against a default Claude group (rate 2.0, live 7.2, mult 1.0).
//
// `provider` is the canonical provider id ("anthropic" | "openai"). For any
// other provider the function falls back to a 1.0 ratio (i.e. official cost).
func Cost(catalog *pricing.Catalog, provider, model string, counts usage.Counts, group *db.PricingGroup, liveCNYPerUSD float64) float64 {
	official := catalog.Cost(provider, model, counts)
	if group == nil || liveCNYPerUSD <= 0 {
		return official
	}
	var pegRMB, mult float64
	switch strings.ToLower(provider) {
	case pricing.ProviderOpenAI:
		pegRMB = group.CodexRMBPerUSD
		mult = group.CodexMultiplier
	case pricing.ProviderAnthropic:
		pegRMB = group.ClaudeRMBPerUSD
		mult = group.ClaudeMultiplier
	default:
		pegRMB = group.ClaudeRMBPerUSD
		mult = group.ClaudeMultiplier
	}
	if pegRMB <= 0 {
		pegRMB = 2.0
	}
	if mult <= 0 {
		mult = 1.0
	}
	return official * (pegRMB / liveCNYPerUSD) * mult
}
