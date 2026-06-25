package adapter

import (
	"testing"

	"github.com/wjsoj/CPA-Claude/internal/server"
	"github.com/wjsoj/cc-core/auth"
)

// MultiplierFor resolves the billing rate from the token's billing workspace:
// 0 = standard default (0.3 claude / 0.05 codex); a custom value wins.
func TestMultiplierForWorkspace(t *testing.T) {
	a := &Adapter{}
	eq := func(got, want float64, msg string) {
		if got != want {
			t.Fatalf("%s: got %v want %v", msg, got, want)
		}
	}

	// Personal workspace (0,0) → standard defaults.
	eq(a.MultiplierFor(server.SaaSTokenInfo{}, auth.ProviderAnthropic), 0.3, "personal claude default")
	eq(a.MultiplierFor(server.SaaSTokenInfo{}, auth.ProviderOpenAI), 0.05, "personal codex default")

	// Enterprise workspace with a custom (discounted) rate.
	ent := server.SaaSTokenInfo{ClaudeMultiplier: 0.25, CodexMultiplier: 0.04}
	eq(a.MultiplierFor(ent, auth.ProviderAnthropic), 0.25, "enterprise claude")
	eq(a.MultiplierFor(ent, auth.ProviderOpenAI), 0.04, "enterprise codex")

	// Partial: only claude set → codex still falls back to its default.
	partial := server.SaaSTokenInfo{ClaudeMultiplier: 0.2}
	eq(a.MultiplierFor(partial, auth.ProviderAnthropic), 0.2, "partial claude")
	eq(a.MultiplierFor(partial, auth.ProviderOpenAI), 0.05, "partial codex falls back")
}
