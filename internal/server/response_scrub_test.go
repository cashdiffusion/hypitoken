package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/mimicry"
)

func formatUnix(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

// preparedForTest builds the prepared result doForwardPrepared would otherwise
// have been handed by the retry loop's preflight.
func preparedForTest(t *testing.T, cred *auth.Auth, body []byte) mimicry.BodyTransformResult {
	t.Helper()
	prepared, err := prepareClaudeOAuthBody(body, "claude-haiku-4-5-20251001", cred, mimicry.SimIdentity{
		AccountKey:  cred.AccountKey(),
		AccountUUID: cred.AccountUUIDValue(),
		ClientToken: "tok-abcdef123456",
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return prepared
}

// Forwarding upstream response headers verbatim handed the caller our pool's
// operational state — the serving account's quota utilisation, its window reset
// timestamps, our organization and workspace UUIDs, the upstream request id.
// The allowlist lives in cc-core/downstream; this pins that it is actually
// wired into the path a successful response takes.
func TestResponseHeadersDoNotLeakPoolState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("Anthropic-Ratelimit-Unified-Status", "allowed")
		h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.83")
		h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1786123800")
		h.Set("Anthropic-Organization-Id", "bf62f90e-ff9c-4d95-a554-17835658b5ef")
		h.Set("Anthropic-Workspace-Id", "wrkspc_01Mx5eXmqPciXqAJUQDyHRAQ")
		h.Set("Request-Id", "req_011CdoZnTHdYogjzJ6Wuzf6Y")
		h.Set("Cf-Ray", "a2770e297a19f3ec-LAX")
		h.Set("Server", "cloudflare")
		// Non-retryable, so it is delivered rather than withheld, without
		// needing the billing machinery a 200 would.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"nope"}}`))
	}))
	defer upstream.Close()

	cred := oauthTestCred()
	s := newDoForwardTestServer(t, upstream.URL, cred)
	c, w := newMessagesContext(t, haikuBody)

	s.doForwardPrepared(c, cred, "/v1/messages", haikuBody, false,
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "slot-1", "client", time.Now(), 1, false,
		preparedForTest(t, cred, haikuBody))

	for _, leaked := range []string{
		"Anthropic-Ratelimit-Unified-Status",
		"Anthropic-Ratelimit-Unified-5h-Utilization",
		"Anthropic-Ratelimit-Unified-5h-Reset",
		"Anthropic-Organization-Id",
		"Anthropic-Workspace-Id",
		"Request-Id",
		"Cf-Ray",
		"Server",
	} {
		if got := w.Header().Get(leaked); got != "" {
			t.Errorf("%s reached the client with %q", leaked, got)
		}
	}
	if got := w.Header().Get("Content-Type"); got == "" {
		t.Error("Content-Type was dropped; the client cannot parse the body without it")
	}
}

// The client must still be able to back off. Upstream sends no Retry-After
// here, so it has to be derived from the reset timestamps before those are
// dropped — 3h away, hence capped at an hour.
func TestWithheldErrorStillCarriesBackoff(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, w := newMessagesContext(t, haikuBody)

	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-Status", "rejected")
	h.Set("Anthropic-Ratelimit-Unified-Reset", formatUnix(time.Now().Add(3*time.Hour)))
	h.Set("Anthropic-Organization-Id", "bf62f90e-ff9c-4d95-a554-17835658b5ef")

	copySafeRetryHeaders(c, h)

	if got := w.Header().Get("Retry-After"); got != "3600" {
		t.Errorf("Retry-After = %q, want a synthesized 3600", got)
	}
	for _, leaked := range []string{"Anthropic-Ratelimit-Unified-Status", "Anthropic-Ratelimit-Unified-Reset", "Anthropic-Organization-Id"} {
		if got := w.Header().Get(leaked); got != "" {
			t.Errorf("%s reached the client with %q", leaked, got)
		}
	}
	// The synthesized error body is ours, so the upstream Content-Type must not
	// ride along and misdescribe it.
	if got := w.Header().Get("Content-Type"); got == "text/event-stream" {
		t.Error("upstream Content-Type was copied onto our own error body")
	}
}
