package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/usage"
)

// deadUpstreamURL returns the URL of a server that is guaranteed not to accept
// connections: a real httptest listener, closed before the test runs. The
// address stays reserved for the process, so a dial against it fails fast and
// deterministically instead of hanging or, worse, reaching somebody else.
func deadUpstreamURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	return url
}

// TestAPIKeyTransportErrorIsWithheldAndRetried covers the one real gap found
// while auditing what actually retries on a relay API key.
//
// A transport error means the relay was never reached: no status, no body, no
// verdict on this request. It is the single most likely failure for a
// different credential to survive, because relays sit behind different hosts
// and proxies. The Claude OAuth path and both Codex paths already rotated on
// it; this one wrote a bare 502 straight to the client and gave up on the
// first connection error, which is how a single relay's TLS handshake timeout
// became a customer-visible failure while healthy credentials sat idle.
func TestAPIKeyTransportErrorIsWithheldAndRetried(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cred := apiKeyTestCred("relayDown")
	s := newDoForwardTestServer(t, deadUpstreamURL(t), cred)
	c, w := newMessagesContext(t, haikuBody)

	retry, done, deferred := s.doForwardAnthropicAPIKey(c, cred, "/v1/messages", haikuBody, false,
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "client", time.Now(), 1)

	if !retry || done {
		t.Fatalf("an unreachable relay must roll back to the failover loop; got retry=%v done=%v", retry, done)
	}
	if deferred == nil {
		t.Fatal("the transport failure must be withheld so a later credential can still succeed")
	}
	if deferred.status != http.StatusBadGateway {
		t.Fatalf("deferred.status = %d, want 502", deferred.status)
	}
	if deferred.authKind != "apikey" || deferred.authID != cred.ID {
		t.Fatalf("deferred must identify the credential that failed; got id=%q kind=%q", deferred.authID, deferred.authKind)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("nothing may reach the client while other credentials remain untried; got %q", w.Body.String())
	}
	if _, _, _, consecutive := cred.HealthSnapshot(); consecutive != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1 — an unreachable relay counts toward health", consecutive)
	}
}

// TestAPIKeyStreamBrokenBeforeFirstByteIsRetried pins down behaviour that was
// already correct but is easy to break: a relay that answers 200 with an SSE
// content type and then closes without emitting a single event — what it does
// when its own backend pool came up empty — must be retried, not passed off as
// a successful empty response.
//
// The cover comes from validateAnthropicResponse, not from the streaming code:
// a body with no bytes cannot look like an Anthropic payload, so the exchange
// is demoted to a 502 contract violation and withheld before the stream branch
// is ever reached. Worth a test precisely because that is not obvious from
// reading the streaming branch, which has no failover of its own — and needs
// none: once the relay has emitted a byte the client is committed.
func TestAPIKeyStreamBrokenBeforeFirstByteIsRetried(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// and nothing else — the connection ends with an empty body.
	}))
	defer upstream.Close()

	cred := apiKeyTestCred("relayEmptyStream")
	s := newDoForwardTestServer(t, upstream.URL, cred)
	c, w := newMessagesContext(t, haikuBody)

	retry, done, deferred := s.doForwardAnthropicAPIKey(c, cred, "/v1/messages", haikuBody, true,
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "client", time.Now(), 1)

	if !retry || done {
		t.Fatalf("a stream that produced nothing must be retried; got retry=%v done=%v", retry, done)
	}
	if deferred == nil || deferred.status != http.StatusBadGateway {
		t.Fatalf("the empty stream must be withheld as a 502; got %+v", deferred)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("no body may be committed for a stream that never started; got %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "" {
		t.Fatalf("headers must not commit before the first relayed byte; Content-Type = %q", ct)
	}
	if _, _, _, consecutive := cred.HealthSnapshot(); consecutive != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1", consecutive)
	}
}

// TestAPIKeyStreamStillReachesClient is the happy-path counterweight to the two
// tests above: the API-key relay had no test asserting that an ordinary stream
// reaches the client intact, so nothing stopped a failover change from
// withholding a perfectly good response.
func TestAPIKeyStreamStillReachesClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message_start\n" +
			`data: {"type":"message_start","message":{"usage":{"input_tokens":7,"output_tokens":0}}}` + "\n\n" +
			"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n\n"))
	}))
	defer upstream.Close()

	cred := apiKeyTestCred("relayOK")
	s := newDoForwardTestServer(t, upstream.URL, cred)
	s.usage = usage.OpenInMemory()
	c, w := newMessagesContext(t, haikuBody)

	// clientToken empty keeps the billing gate (which needs a pricing catalog)
	// out of a test that is only about the header/stream contract.
	retry, done, deferred := s.doForwardAnthropicAPIKey(c, cred, "/v1/messages", haikuBody, true,
		"claude-haiku-4-5-20251001", "", "client", time.Now(), 1)

	if retry || deferred != nil {
		t.Fatalf("a complete stream must not be retried; got retry=%v deferred=%+v", retry, deferred)
	}
	if !done {
		t.Fatal("a complete stream must finish the request")
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if body := w.Body.String(); body == "" {
		t.Fatal("the relayed stream must reach the client")
	}
}
