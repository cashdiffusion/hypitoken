// Package billing is the single source of truth for translating an
// upstream-side (official) cost into the dollar amount that gets deducted
// from the user's wallet and recorded in the request log.
//
// Formula:
//
//	billed = official * multiplier
//
// `multiplier` is the per-(group × provider) coefficient stored on the
// pricing-groups table (claude_multiplier / codex_multiplier). The operator
// sets it from the admin panel; defaults are claude=0.3 and codex=0.05
// (both <1 so the user pays less than the upstream rack rate). Passing a
// non-positive multiplier falls back to 1.0 — i.e. the official cost is
// charged as-is — so callers do not need to special-case missing config.
package billing

import (
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/usage"
)

// Charge looks up the official price for (provider, model, counts) and
// applies the multiplier. The result is what the user pays.
func Charge(catalog *pricing.Catalog, provider, model string, counts usage.Counts, multiplier float64) float64 {
	if catalog == nil {
		return 0
	}
	return ChargeFromOfficial(catalog.Cost(provider, model, counts), multiplier)
}

// ChargeFromOfficial scales an already-computed official cost. Use this
// from the request hot path where Cost() has already been called for the
// request log so the catalog isn't queried twice.
func ChargeFromOfficial(official, multiplier float64) float64 {
	if multiplier <= 0 {
		multiplier = 1.0
	}
	return official * multiplier
}
