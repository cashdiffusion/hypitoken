// Package pricing maps (provider, model) pairs to per-token USD cost and
// computes request cost from a usage.Counts payload.
//
// The cost formula is:
//
//	cost = (input*P_in + output*P_out + cacheRead*P_cr + cacheCreate*P_cw) / 1e6
//
// The built-in catalog covers current Claude models plus the OpenAI / Codex
// models that match CLIProxyAPI's subscription tiers. Extra models or custom
// overrides can be supplied via config.yaml:
//
//	pricing:
//	  default:
//	    input_per_1m: 3.00
//	    output_per_1m: 15.00
//	    cache_read_per_1m: 0.30
//	    cache_create_per_1m: 3.75
//	  provider_defaults:
//	    openai:
//	      input_per_1m: 1.25
//	      output_per_1m: 10.0
//	  models:
//	    anthropic/claude-opus-4-7:
//	      input_per_1m: 5.0
//	      output_per_1m: 25.0
//	    openai/gpt-5.3-codex:
//	      input_per_1m: 1.25
//	      output_per_1m: 10.0
//
// Legacy bare-model keys (e.g. `claude-opus-4-6:`) are treated as
// `anthropic/<model>` on load so existing configs keep working.
package pricing

import (
	"strings"

	"github.com/wjsoj/CPA-Claude/internal/usage"
)

// Canonical provider ids. Kept as constants rather than importing from
// internal/auth to avoid a cyclic dependency — pricing is a leaf package.
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)

// ModelPrice is the USD price per 1M tokens for each token class. CacheRead
// and CacheCreate are priced separately because Anthropic charges them
// differently (cache_read ~0.1× input, cache_create ~1.25× input). For
// providers without cache semantics (OpenAI), we put the "cached_tokens"
// figure from the response into CacheReadTokens and use CacheReadPer1M for
// its discounted rate — typically ~0.25× input on OpenAI's API.
type ModelPrice struct {
	InputPer1M       float64 `yaml:"input_per_1m" json:"input_per_1m"`
	OutputPer1M      float64 `yaml:"output_per_1m" json:"output_per_1m"`
	CacheReadPer1M   float64 `yaml:"cache_read_per_1m" json:"cache_read_per_1m"`
	CacheCreatePer1M float64 `yaml:"cache_create_per_1m" json:"cache_create_per_1m"`
}

// Cost returns USD for the given token counts under this price card.
func (p ModelPrice) Cost(c usage.Counts) float64 {
	return (float64(c.InputTokens)*p.InputPer1M +
		float64(c.OutputTokens)*p.OutputPer1M +
		float64(c.CacheReadTokens)*p.CacheReadPer1M +
		float64(c.CacheCreateTokens)*p.CacheCreatePer1M) / 1_000_000
}

// Config is the YAML shape of the `pricing` section.
type Config struct {
	// Default is the last-resort price card when nothing else matches.
	Default ModelPrice `yaml:"default"`
	// ProviderDefaults supply per-provider fallbacks used when a model lookup
	// misses in Models (e.g. any OpenAI model without an explicit entry gets
	// ProviderDefaults["openai"]).
	ProviderDefaults map[string]ModelPrice `yaml:"provider_defaults"`
	// Models maps "<provider>/<model>" (or bare model, defaulting to
	// anthropic) to its price card.
	Models map[string]ModelPrice `yaml:"models"`
}

// Catalog resolves (provider, model) to a price card, with four-level
// fallback: exact → prefix → provider default → global default.
type Catalog struct {
	defaultPrice     ModelPrice
	providerDefaults map[string]ModelPrice
	models           map[string]ModelPrice // key = "provider/model" (lowercase)
}

// NewCatalog merges the user config (may be zero-valued) on top of the
// built-in defaults so callers always get a sensible price for common models.
func NewCatalog(c Config) *Catalog {
	cat := &Catalog{
		defaultPrice:     defaultModelPrice(),
		providerDefaults: make(map[string]ModelPrice),
		models:           make(map[string]ModelPrice, len(builtIn)+len(c.Models)),
	}
	for k, v := range builtInProviderDefaults {
		cat.providerDefaults[k] = v
	}
	for k, v := range builtIn {
		cat.models[k] = v
	}
	// User overrides last so they can shadow built-ins.
	for k, v := range c.ProviderDefaults {
		cat.providerDefaults[strings.ToLower(strings.TrimSpace(k))] = v
	}
	for k, v := range c.Models {
		cat.models[normalizeModelKey(k)] = v
	}
	if nonZero(c.Default) {
		cat.defaultPrice = c.Default
	}
	return cat
}

// Lookup returns the price card for a (provider, model) pair. Matching is
// case-insensitive and tolerates well-known prefix matches (e.g. a suffix-
// dated Claude model falls back to its undated base entry). Empty provider
// is treated as Anthropic for backward compatibility with legacy callers.
func (c *Catalog) Lookup(provider, model string) ModelPrice {
	prov := canonicalProvider(provider)
	m := strings.ToLower(strings.TrimSpace(model))
	// Strip a trailing "(value)" thinking suffix — CLIProxyAPI's convention
	// for encoding reasoning effort in the model name. "gpt-5.3-codex(high)"
	// bills the same as "gpt-5.3-codex".
	if strings.HasSuffix(m, ")") {
		if i := strings.LastIndex(m, "("); i > 0 {
			m = strings.TrimSpace(m[:i])
		}
	}
	if m != "" {
		full := prov + "/" + m
		if p, ok := c.models[full]; ok {
			return p
		}
		// Prefix fallback: trim trailing "-segment"s off the model name and
		// retry. Covers "claude-sonnet-4-6-20260401" → "claude-sonnet-4-6".
		for i := strings.LastIndex(m, "-"); i > 0; i = strings.LastIndex(m[:i], "-") {
			if p, ok := c.models[prov+"/"+m[:i]]; ok {
				return p
			}
		}
	}
	if p, ok := c.providerDefaults[prov]; ok && nonZero(p) {
		return p
	}
	return c.defaultPrice
}

// Cost is a convenience shortcut — Lookup(provider, model).Cost(counts).
func (c *Catalog) Cost(provider, model string, counts usage.Counts) float64 {
	return c.Lookup(provider, model).Cost(counts)
}

// Models returns a copy of the registered model → price map. Keys are in
// canonical "provider/model" form.
func (c *Catalog) Models() map[string]ModelPrice {
	out := make(map[string]ModelPrice, len(c.models))
	for k, v := range c.models {
		out[k] = v
	}
	return out
}

// Default returns the global fallback price card (provider-agnostic).
func (c *Catalog) Default() ModelPrice { return c.defaultPrice }

// ProviderDefaults returns a copy of the per-provider fallback cards.
func (c *Catalog) ProviderDefaults() map[string]ModelPrice {
	out := make(map[string]ModelPrice, len(c.providerDefaults))
	for k, v := range c.providerDefaults {
		out[k] = v
	}
	return out
}

// normalizeModelKey canonicalizes a user-supplied pricing.models key. Bare
// model names (no "/") are assumed to be Anthropic — matches pre-multi-
// provider configs. Whitespace and case are normalized.
func normalizeModelKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	if k == "" {
		return ""
	}
	if !strings.Contains(k, "/") {
		return ProviderAnthropic + "/" + k
	}
	return k
}

func canonicalProvider(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "", "anthropic", "claude":
		return ProviderAnthropic
	case "openai", "codex", "chatgpt":
		return ProviderOpenAI
	}
	return strings.ToLower(strings.TrimSpace(p))
}

func nonZero(p ModelPrice) bool {
	return p.InputPer1M != 0 || p.OutputPer1M != 0 || p.CacheReadPer1M != 0 || p.CacheCreatePer1M != 0
}

func defaultModelPrice() ModelPrice {
	// Global fallback when neither an explicit model nor a provider default
	// matches. Set to Claude Sonnet — closest to average across our mix.
	return ModelPrice{
		InputPer1M:       3.00,
		OutputPer1M:      15.00,
		CacheReadPer1M:   0.30,
		CacheCreatePer1M: 3.75,
	}
}

// builtInProviderDefaults: per-provider fallback when a specific model isn't
// in the catalog. For OpenAI we use gpt-5 flagship pricing so unknown Codex
// models don't undercharge; for Anthropic we use Sonnet (matches global).
var builtInProviderDefaults = map[string]ModelPrice{
	ProviderAnthropic: {
		InputPer1M:       3.00,
		OutputPer1M:      15.00,
		CacheReadPer1M:   0.30,
		CacheCreatePer1M: 3.75,
	},
	ProviderOpenAI: {
		InputPer1M:       1.25,
		OutputPer1M:      10.00,
		CacheReadPer1M:   0.125, // OpenAI cached input is ~0.1× input
		CacheCreatePer1M: 0,
	},
}

// builtIn is the stock (provider, model) → price catalog. Anthropic values
// track their published pricing (cache_create = 1.25× input, cache_read =
// 0.1× input). OpenAI values approximate OpenAI's public API pricing; they
// are applied to both ChatGPT-subscription OAuth credentials (notional cost
// for weekly-limit enforcement) and BYOK API-key credentials (real cost).
var builtIn = map[string]ModelPrice{
	// ─── Anthropic ──────────────────────────────────────────────────────
	ProviderAnthropic + "/claude-haiku-4-5-20251001": {
		InputPer1M:       1.00,
		OutputPer1M:      5.00,
		CacheReadPer1M:   0.10,
		CacheCreatePer1M: 1.25,
	},
	ProviderAnthropic + "/claude-haiku-4-5": {
		InputPer1M:       1.00,
		OutputPer1M:      5.00,
		CacheReadPer1M:   0.10,
		CacheCreatePer1M: 1.25,
	},
	// Anthropic Opus 4.x official list price: $5 / $25 / $0.50 / $6.25 per 1M
	// for input / output / cache_read / cache_write. The 0.3× user-facing
	// discount is applied via group/token multiplier, not by scaling this
	// table — keep these at the upstream rate.
	ProviderAnthropic + "/claude-opus-4-6": {
		InputPer1M:       5.00,
		OutputPer1M:      25.00,
		CacheReadPer1M:   0.50,
		CacheCreatePer1M: 6.25,
	},
	ProviderAnthropic + "/claude-opus-4-7": {
		InputPer1M:       5.00,
		OutputPer1M:      25.00,
		CacheReadPer1M:   0.50,
		CacheCreatePer1M: 6.25,
	},
	ProviderAnthropic + "/claude-sonnet-4-6": {
		InputPer1M:       3.00,
		OutputPer1M:      15.00,
		CacheReadPer1M:   0.30,
		CacheCreatePer1M: 3.75,
	},

	// ─── OpenAI / Codex ────────────────────────────────────────────────
	// Names match the tier-specific models served through the Codex CLI
	// (see CLIProxyAPI/internal/registry/models/models.json). OpenAI has
	// not published list prices for every "5.x" codex-tier label; values
	// below are sized by the intended position in the lineup:
	//
	//   5.2  – previous-gen flagship       → gpt-5-class pricing
	//   5.3-codex / spark – coding-tuned  → gpt-5-class pricing
	//   5.4  – current flagship           → slight premium
	//   5.4-mini – fast/cheap             → gpt-5-mini-class pricing
	//   5.5  – frontier                   → frontier premium
	ProviderOpenAI + "/gpt-5.2": {
		InputPer1M:       1.25,
		OutputPer1M:      10.00,
		CacheReadPer1M:   0.125,
	},
	ProviderOpenAI + "/gpt-5.3-codex": {
		InputPer1M:       1.25,
		OutputPer1M:      10.00,
		CacheReadPer1M:   0.125,
	},
	ProviderOpenAI + "/gpt-5.3-codex-spark": {
		InputPer1M:       1.25,
		OutputPer1M:      10.00,
		CacheReadPer1M:   0.125,
	},
	ProviderOpenAI + "/gpt-5.4": {
		InputPer1M:       1.50,
		OutputPer1M:      12.00,
		CacheReadPer1M:   0.15,
	},
	ProviderOpenAI + "/gpt-5.4-mini": {
		InputPer1M:       0.25,
		OutputPer1M:      2.00,
		CacheReadPer1M:   0.025,
	},
	ProviderOpenAI + "/gpt-5.5": {
		InputPer1M:       2.50,
		OutputPer1M:      20.00,
		CacheReadPer1M:   0.25,
	},
	// We expose only Codex-tier models (gpt-5.x); the gateway does not
	// ship a generic OpenAI BYOK surface, so gpt-5 / gpt-5-mini / gpt-5-nano
	// / gpt-4o entries would never match anything real.
}
