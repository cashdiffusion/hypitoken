package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
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

// Generic Haiku body used across failover and strict-synthesis tests.
var haikuBody = []byte(`{"model":"claude-haiku-4-5-20251001","messages":[{"role":"user","content":"hi"}]}`)

func TestForwardPreparationFailureFallsBackToAPIKeyWithOriginalBodyAndAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var oauthCalls, apiKeyCalls int
	var apiKeyBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			oauthCalls++
		}
		if r.Header.Get("x-api-key") != "" {
			apiKeyCalls++
			apiKeyBody, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"test stop"}}`))
	}))
	defer upstream.Close()

	oauthCred := oauthTestCred()
	oauthCred.AccountUUID = "" // deterministic local preparation failure
	oauthCredSecond := oauthTestCredID("credB", "tokenB")
	oauthCredSecond.AccountUUID = ""
	apiKeyCred := apiKeyTestCred("fallback")
	logDir := t.TempDir()
	writer, err := requestlog.Open(logDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		cfg:           &config.Config{AnthropicBaseURL: upstream.URL, UseUTLS: false},
		pool:          auth.NewPool([]*auth.Auth{oauthCred, oauthCredSecond}, []*auth.Auth{apiKeyCred}, 10*time.Minute, false, ""),
		switchTracker: thinkingsig.NewSwitchTracker(),
		reqLog:        writer,
	}
	c, recorder := newMessagesContext(t, haikuBody)
	s.forwardWithFailover(c, auth.ProviderAnthropic, "/v1/messages",
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "", "client", "slot-1", haikuBody, false, time.Now())
	writer.Close()

	if oauthCalls != 0 {
		t.Fatalf("preparation failure reached OAuth upstream %d times", oauthCalls)
	}
	if apiKeyCalls != 1 || !bytes.Equal(apiKeyBody, haikuBody) {
		t.Fatalf("API-key fallback calls=%d body=%s want original=%s", apiKeyCalls, apiKeyBody, haikuBody)
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("fallback response status=%d", recorder.Code)
	}
	files, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, file := range files {
		raw, readErr := os.ReadFile(logDir + "/" + file.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, line := range bytes.Split(raw, []byte{'\n'}) {
			var record requestlog.Record
			if json.Unmarshal(line, &record) != nil {
				continue
			}
			if record.AttemptOnly && record.ClaudeAudit != nil && record.ClaudeAudit.PreparationFailed {
				found = record.ClaudeAudit.RequestClass == "generic" &&
					record.ClaudeAudit.PreparationError == "missing_account_uuid" &&
					record.ClaudeAudit.Fallback == "apikey" &&
					record.ClaudeAudit.BodyBytes == len(haikuBody) &&
					len(record.ClaudeAudit.BodySHA256) == 64 &&
					record.ClaudeAudit.SessionBinding == "unavailable" &&
					record.ClaudeAudit.BillingValidation == "failed"
			}
		}
	}
	if !found {
		t.Fatal("missing structured preparation-fallback audit")
	}
}

// Regression for the production 400 burst: a Generic client can send an
// OpenAI-style messages[0].role=system. The OAuth path must move that prompt
// to top-level system, never ship the illegal role, and never pass ingress
// headers or identity through to the selected account.
func TestForwardGenericSystemMessageIsNormalizedBeforeOAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-sonnet-5","metadata":{"user_id":"downstream-user"},"messages":[{"role":"system","content":"be a careful coding assistant"},{"role":"user","content":"question"}]}`)
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Downstream-Trace") != "" || r.Header.Get("Traceparent") != "" {
			t.Fatalf("generic ingress headers leaked: %v", r.Header)
		}
		upstreamBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var prepared struct {
			System []struct {
				Text string `json:"text"`
			} `json:"system"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
			Metadata struct {
				UserID string `json:"user_id"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(upstreamBody, &prepared); err != nil {
			t.Fatal(err)
		}
		if len(prepared.System) < 3 || prepared.System[2].Text != "be a careful coding assistant" {
			t.Fatalf("system prompt was not migrated: %s", upstreamBody)
		}
		for _, message := range prepared.Messages {
			if message.Role == "system" {
				t.Fatalf("illegal system message reached OAuth: %s", upstreamBody)
			}
		}
		if strings.Contains(prepared.Metadata.UserID, "downstream-user") {
			t.Fatalf("downstream identity leaked: %s", upstreamBody)
		}
		var identity struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(prepared.Metadata.UserID), &identity); err != nil {
			t.Fatal(err)
		}
		if identity.SessionID == "" || r.Header.Get("X-Claude-Code-Session-Id") != identity.SessionID {
			t.Fatalf("header/body session mismatch: header=%q identity=%+v", r.Header.Get("X-Claude-Code-Session-Id"), identity)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"test stop"}}`))
	}))
	defer upstream.Close()

	credential := oauthTestCred()
	s := newDoForwardTestServer(t, upstream.URL, credential)
	c, recorder := newMessagesContext(t, body)
	c.Request.Header.Set("X-Downstream-Trace", "do-not-forward")
	c.Request.Header.Set("Traceparent", "00-downstream")
	s.forwardWithFailover(c, auth.ProviderAnthropic, "/v1/messages", "claude-sonnet-5", "tok-abcdef123456", "", "client", "slot-1", body, false, time.Now())

	if calls != 1 || recorder.Code != http.StatusBadRequest {
		t.Fatalf("calls=%d status=%d", calls, recorder.Code)
	}
}

func TestForwardUnsafeGenericBodyReturnsLocal400WithoutFallbackOrOAuthRotation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-sonnet-5","messages":[{"role":"system","content":[]},{"role":"user","content":"question"}]}`)
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	oauthA := oauthTestCredID("credA", "tokenA")
	oauthB := oauthTestCredID("credB", "tokenB")
	apiKey := apiKeyTestCred("fallback")
	logDir := t.TempDir()
	writer, err := requestlog.Open(logDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		cfg:           &config.Config{AnthropicBaseURL: upstream.URL, UseUTLS: false},
		pool:          auth.NewPool([]*auth.Auth{oauthA, oauthB}, []*auth.Auth{apiKey}, 10*time.Minute, false, ""),
		switchTracker: thinkingsig.NewSwitchTracker(),
		reqLog:        writer,
	}
	c, recorder := newMessagesContext(t, body)
	s.forwardWithFailover(c, auth.ProviderAnthropic, "/v1/messages", "claude-sonnet-5", "tok-abcdef123456", "", "client", "slot-1", body, false, time.Now())
	writer.Close()

	if upstreamCalls != 0 || recorder.Code != http.StatusBadRequest || recorder.Header().Get("X-Error-Code") != "invalid_request" {
		t.Fatalf("upstream=%d status=%d code=%q", upstreamCalls, recorder.Code, recorder.Header().Get("X-Error-Code"))
	}
	files, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	var audit *requestlog.ClaudeAudit
	for _, file := range files {
		data, readErr := os.ReadFile(logDir + "/" + file.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			var record requestlog.Record
			if json.Unmarshal(line, &record) == nil && record.ClaudeAudit != nil && record.ClaudeAudit.PreparationFailed {
				audit = record.ClaudeAudit
			}
		}
	}
	if audit == nil || audit.Fallback != "none" || audit.PreparationError != "invalid_directive_structure" ||
		audit.PreparationFailures != 1 || audit.ClientHash == "" || len(audit.BodySHA256) != 64 {
		t.Fatalf("missing local-rejection audit: %+v", audit)
	}
}

func TestForwardGenuineMissingBetaFallsBackToAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var oauthCalls, apiKeyCalls int
	var apiKeyBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			oauthCalls++
		}
		if r.Header.Get("x-api-key") != "" {
			apiKeyCalls++
			apiKeyBody, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"test stop"}}`))
	}))
	defer upstream.Close()

	oauthCred := oauthTestCred()
	apiKeyCred := apiKeyTestCred("fallback-genuine")
	s := &Server{
		cfg:           &config.Config{AnthropicBaseURL: upstream.URL, UseUTLS: false},
		pool:          auth.NewPool([]*auth.Auth{oauthCred}, []*auth.Auth{apiKeyCred}, 10*time.Minute, false, ""),
		switchTracker: thinkingsig.NewSwitchTracker(),
	}
	body := genuineServerPolicyBody("claude-sonnet-5")
	c, recorder := newMessagesContext(t, body) // deliberately no Anthropic-Beta
	s.forwardWithFailover(c, auth.ProviderAnthropic, "/v1/messages",
		"claude-sonnet-5", "tok-abcdef123456", "", "client", identityPolicySession, body, false, time.Now())

	if oauthCalls != 0 || apiKeyCalls != 1 || !bytes.Equal(apiKeyBody, body) {
		t.Fatalf("oauth=%d apikey=%d body_preserved=%v", oauthCalls, apiKeyCalls, bytes.Equal(apiKeyBody, body))
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("fallback response status=%d", recorder.Code)
	}
}

func TestForwardPreparationFailureWithoutAPIKeyReturns503WithoutOAuthCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	oauthCred := oauthTestCred()
	oauthCred.AccountUUID = ""
	s := &Server{
		cfg:           &config.Config{AnthropicBaseURL: upstream.URL, UseUTLS: false},
		pool:          auth.NewPool([]*auth.Auth{oauthCred}, nil, 10*time.Minute, false, ""),
		switchTracker: thinkingsig.NewSwitchTracker(),
	}
	c, recorder := newMessagesContext(t, haikuBody)
	s.forwardWithFailover(c, auth.ProviderAnthropic, "/v1/messages",
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "", "client", "slot-1", haikuBody, false, time.Now())

	if upstreamCalls != 0 {
		t.Fatalf("preparation failure reached upstream %d times", upstreamCalls)
	}
	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("X-Error-Code") != "service_temporarily_unavailable" {
		t.Fatalf("status=%d error_code=%q body=%q", recorder.Code, recorder.Header().Get("X-Error-Code"), recorder.Body.String())
	}
}

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

	retry, done, deferred := s.doForwardPrepared(c, cred, "/v1/messages", haikuBody, false,
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "slot-1", "client", time.Now(), 1, false, mimicry.BodyTransformResult{})

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
	// the loop stops at B and returns the gateway's sanitized client error.
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

	if w.Code != http.StatusBadRequest || w.Header().Get("X-Error-Code") != "invalid_request" || strings.Contains(w.Body.String(), "served_by") {
		t.Fatalf("client should have received B's response after A's quota 429; got code=%d body=%q", w.Code, w.Body.String())
	}
	if !credA.IsQuotaExceeded(time.Now()) {
		t.Fatal("credA should be cooled down so the pool stops routing to it")
	}
}

// TestForwardWithFailoverSurfacesRealErrorWhenExhausted verifies that when
// every credential is quota-limited, the client receives a stable, sanitized
// 429 rather than the raw service body or service-specific headers.
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
	if w.Header().Get("X-Error-Code") != "service_rate_limited" || strings.Contains(w.Body.String(), "Anthropic") {
		t.Fatal("surfaced 429 should use the sanitized public error taxonomy")
	}
}

// TestDoForwardForwardsNonRetryableErrors verifies that request-level and
// upstream-wide errors are sanitized without failover:
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

			retry, done, deferred := s.doForwardPrepared(c, cred, "/v1/messages", haikuBody, false,
				"claude-haiku-4-5-20251001", "tok-abcdef123456", "slot-1", "client", time.Now(), 1, false, mimicry.BodyTransformResult{})

			if retry || !done {
				t.Fatalf("non-retryable %d should yield (retry=false, done=true); got retry=%v done=%v", tc.status, retry, done)
			}
			if deferred != nil {
				t.Fatalf("non-retryable %d must not withhold a deferred response", tc.status)
			}
			if w.Code != tc.status {
				t.Fatalf("client status = %d, want %d", w.Code, tc.status)
			}
			if w.Header().Get("X-Error-Code") == "" || w.Body.String() == tc.body {
				t.Fatalf("client body should be standardized and sanitized, got %q", w.Body.String())
			}
			if cred.IsQuotaExceeded(time.Now()) {
				t.Fatalf("non-retryable %d must not cool down the credential", tc.status)
			}
		})
	}
}
