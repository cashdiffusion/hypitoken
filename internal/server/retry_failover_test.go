package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/thinkingsig"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

// newDoForwardTestServer builds the smallest Server that can drive doForward
// for an OAuth credential against a mock upstream. usage/pricing/saas/reqLog
// stay nil — only the >=400 (withhold / forward) paths are exercised, and
// those never touch the billing machinery. sidecar stays nil too: Notify is
// nil-receiver-safe and returns no bootstrap channel, so there is no wait.
func newDoForwardTestServer(t *testing.T, upstreamURL string, cred *auth.Auth) *Server {
	t.Helper()
	return &Server{
		cfg:           &config.Config{AnthropicBaseURL: upstreamURL, UseUTLS: false},
		pool:          auth.NewPool([]*auth.Auth{cred}, nil, 10*time.Minute, false, ""),
		switchTracker: thinkingsig.NewSwitchTracker(),
		sidecar:       nil,
	}
}

func oauthTestCred() *auth.Auth {
	return &auth.Auth{
		ID:          "credA",
		Kind:        auth.KindOAuth,
		Provider:    "anthropic",
		Label:       "credA",
		AccessToken: "test-token",
		AccountUUID: "11111111-1111-1111-1111-111111111111",
		ExpiresAt:   time.Now().Add(2 * time.Hour),
	}
}

func newMessagesContext(t *testing.T, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

// Haiku body skips Claude Code mimicry, keeping the request untouched — the
// failover decision under test is independent of body shaping.
var haikuBody = []byte(`{"model":"claude-haiku-4-5-20251001","messages":[{"role":"user","content":"hi"}]}`)

// TestDoForwardWithholdsRetryableCredentialError verifies the core failover
// contract: a credential-level 429 (quota) is NOT written to the client; it is
// withheld (retry=true, done=false, deferred!=nil) so forward() can switch
// credentials, and the credential is cooled down so the pool stops routing to
// it.
func TestDoForwardWithholdsRetryableCredentialError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reset := strconv.FormatInt(time.Now().Add(3*time.Hour).Unix(), 10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Anthropic-Ratelimit-Unified-Status", "rejected")
		w.Header().Set("Anthropic-Ratelimit-Unified-Reset", reset)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"limit"}}`))
	}))
	defer upstream.Close()

	cred := oauthTestCred()
	s := newDoForwardTestServer(t, upstream.URL, cred)
	c, w := newMessagesContext(t, haikuBody)

	retry, done, deferred := s.doForward(c, cred, "/v1/messages", haikuBody, false,
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "slot-1", "client", time.Now(), 1, false)

	if !retry || done {
		t.Fatalf("retryable 429 should yield (retry=true, done=false); got retry=%v done=%v", retry, done)
	}
	if deferred == nil {
		t.Fatal("retryable 429 must return a deferred response so the loop can surface it if exhausted")
	}
	if deferred.status != http.StatusTooManyRequests {
		t.Fatalf("deferred.status = %d, want 429", deferred.status)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("withheld error must not be written to the client; got %q", w.Body.String())
	}
	if !cred.IsQuotaExceeded(time.Now()) {
		t.Fatal("credential should be marked quota-exceeded so the pool skips it")
	}
}

// oauthTestCredID builds an OAuth credential with a caller-chosen ID and
// access token so a mock upstream can route by Authorization bearer.
func oauthTestCredID(id, token string) *auth.Auth {
	return &auth.Auth{
		ID:          id,
		Kind:        auth.KindOAuth,
		Provider:    "anthropic",
		Label:       id,
		AccessToken: token,
		AccountUUID: "11111111-1111-1111-1111-11111111111" + id[len(id)-1:],
		ExpiresAt:   time.Now().Add(2 * time.Hour),
	}
}

// TestForwardWithFailoverSwitchesCredentialOnQuota is the core money-path
// proof: credential A is at quota (429) and credential B is healthy. The loop
// must transparently switch from A to B so the *client* sees B's response, not
// A's 429 — i.e. failover actually happened end to end.
func TestForwardWithFailoverSwitchesCredentialOnQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reset := strconv.FormatInt(time.Now().Add(3*time.Hour).Unix(), 10)
	// B replies with a distinctive non-2xx body so we can assert the client got
	// B's response (proving rotation) without constructing the usage/pricing
	// billing machinery the <400 success path needs. 400 is non-retryable, so
	// the loop stops at B and forwards it verbatim.
	const bResponse = `{"served_by":"B"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer tokenB" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(bResponse))
			return
		}
		w.Header().Set("Anthropic-Ratelimit-Unified-Status", "rejected")
		w.Header().Set("Anthropic-Ratelimit-Unified-Reset", reset)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"limit"}}`))
	}))
	defer upstream.Close()

	credA := oauthTestCredID("credA", "tokenA")
	credB := oauthTestCredID("credB", "tokenB")
	s := &Server{
		cfg:           &config.Config{AnthropicBaseURL: upstream.URL, UseUTLS: false},
		pool:          auth.NewPool([]*auth.Auth{credA, credB}, nil, 10*time.Minute, false, ""),
		switchTracker: thinkingsig.NewSwitchTracker(),
	}
	c, w := newMessagesContext(t, haikuBody)

	s.forwardWithFailover(c, auth.ProviderAnthropic, "/v1/messages",
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "", "client", "slot-1", haikuBody, false, time.Now())

	if w.Code != http.StatusBadRequest || w.Body.String() != bResponse {
		t.Fatalf("client should have received B's response after A's quota 429; got code=%d body=%q", w.Code, w.Body.String())
	}
	if !credA.IsQuotaExceeded(time.Now()) {
		t.Fatal("credA should be cooled down so the pool stops routing to it")
	}
}

// TestForwardWithFailoverSurfacesRealErrorWhenExhausted verifies that when
// every credential is quota-limited, the client receives the genuine upstream
// 429 (with its rate-limit headers) — NOT a synthetic 503 — so clients back
// off correctly instead of hard-erroring.
func TestForwardWithFailoverSurfacesRealErrorWhenExhausted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reset := strconv.FormatInt(time.Now().Add(3*time.Hour).Unix(), 10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Anthropic-Ratelimit-Unified-Status", "rejected")
		w.Header().Set("Anthropic-Ratelimit-Unified-Reset", reset)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"limit"}}`))
	}))
	defer upstream.Close()

	credA := oauthTestCredID("credA", "tokenA")
	credB := oauthTestCredID("credB", "tokenB")
	s := &Server{
		cfg:           &config.Config{AnthropicBaseURL: upstream.URL, UseUTLS: false},
		pool:          auth.NewPool([]*auth.Auth{credA, credB}, nil, 10*time.Minute, false, ""),
		switchTracker: thinkingsig.NewSwitchTracker(),
	}
	c, w := newMessagesContext(t, haikuBody)

	s.forwardWithFailover(c, auth.ProviderAnthropic, "/v1/messages",
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "", "client", "slot-1", haikuBody, false, time.Now())

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("exhausted pool must surface the real upstream 429, not a synthetic 503; got %d", w.Code)
	}
	if w.Header().Get("Anthropic-Ratelimit-Unified-Status") != "rejected" {
		t.Fatal("surfaced 429 should carry the upstream rate-limit headers so the client backs off correctly")
	}
}

// TestDoForwardForwardsNonRetryableErrors verifies that request-level and
// upstream-wide errors are forwarded to the client verbatim (no failover):
// a generic 400 (client fault — every credential rejects it) and a 503
// (Anthropic-wide weather — retrying just amplifies load).
func TestDoForwardForwardsNonRetryableErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name       string
		status     int
		body       string
		wantQuota  bool
		wantHardFa bool
	}{
		{name: "generic 400 client fault", status: http.StatusBadRequest, body: `{"error":"bad request"}`},
		{name: "503 upstream weather", status: http.StatusServiceUnavailable, body: `{"error":"overloaded"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()

			cred := oauthTestCred()
			s := newDoForwardTestServer(t, upstream.URL, cred)
			c, w := newMessagesContext(t, haikuBody)

			retry, done, deferred := s.doForward(c, cred, "/v1/messages", haikuBody, false,
				"claude-haiku-4-5-20251001", "tok-abcdef123456", "slot-1", "client", time.Now(), 1, false)

			if retry || !done {
				t.Fatalf("non-retryable %d should yield (retry=false, done=true); got retry=%v done=%v", tc.status, retry, done)
			}
			if deferred != nil {
				t.Fatalf("non-retryable %d must not withhold a deferred response", tc.status)
			}
			if w.Code != tc.status {
				t.Fatalf("client status = %d, want %d", w.Code, tc.status)
			}
			if w.Body.String() != tc.body {
				t.Fatalf("client body = %q, want %q (forwarded verbatim)", w.Body.String(), tc.body)
			}
			if cred.IsQuotaExceeded(time.Now()) {
				t.Fatalf("non-retryable %d must not cool down the credential", tc.status)
			}
		})
	}
}
