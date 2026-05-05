package billing

import (
	"github.com/wjsoj/CPA-Claude/internal/pricing"
	"github.com/wjsoj/CPA-Claude/internal/usage"
)

// Cost computes the wallet USD charged for a given request.
//
//	bill = official_cost_usd / billing_rate
//
// billing_rate is "1r = X USD": higher = cheaper for the user.
// If billingRate <= 0 the official cost is returned unchanged.
func Cost(catalog *pricing.Catalog, provider, model string, counts usage.Counts, billingRate float64) float64 {
	official := catalog.Cost(provider, model, counts)
	if billingRate <= 0 {
		return official
	}
	return official / billingRate
}
