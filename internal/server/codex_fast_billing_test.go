package server

import (
	"context"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/usage"
)

// Capture the actual amount handed to the SaaS wallet, then apply the
// workspace rate as the adapter does. No other adapter methods are used here.
type fastChargeSpy struct {
	SaaSAdapter
	official float64
	calls    int
}

func (s *fastChargeSpy) Charge(_ context.Context, _ SaaSTokenInfo, _, _ string, _ usage.Counts, official float64) (float64, error) {
	s.official = official
	s.calls++
	return official * .2, nil
}
func (*fastChargeSpy) MultiplierFor(SaaSTokenInfo, string) float64 { return .2 }
func TestCodexFastWalletMultiplierOnce(t *testing.T) {
	cred := &auth.Auth{ID: "fast-wallet-account", Kind: auth.KindOAuth, Provider: auth.ProviderOpenAI}
	s := codexHTTPTestServer("", cred)
	spy := &fastChargeSpy{}
	s.saas = spy
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	c.Set("saas_info", SaaSTokenInfo{UserID: 1, TokenID: 1})
	s.billCodexWSTurn(c, cred, "gpt-5.5", "fast-wallet", "tester", usage.Counts{InputTokens: 600, OutputTokens: 200, CacheReadTokens: 400, Requests: 1}, time.Second, 1, pricing.CostOptions{ServiceTier: "priority", ResponseServiceTier: "default"})
	// The credential is KindOAuth, and billCodexWSTurn derives CodexOAuth from
	// the credential rather than from the caller — so this is a ChatGPT
	// subscription and the requested "priority" buys no premium. It used to
	// expect .023 (2.5x) here, which charged the wallet for an upstream cost
	// the subscription never incurred.
	if spy.calls != 1 || math.Abs(spy.official-.0092) > 1e-12 {
		t.Fatalf("wallet input %g calls %d", spy.official, spy.calls)
	}
	if got := s.usage.WeeklyCostUSD("fast-wallet"); math.Abs(got-.00184) > 1e-12 {
		t.Fatalf("billed cost %g want .00184", got)
	}
}
