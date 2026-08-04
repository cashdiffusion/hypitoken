package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/thinkingsig"
)

const identityPolicySession = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const identityPolicyMainBeta = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,advisor-tool-2026-03-01,advanced-tool-use-2025-11-20,effort-2025-11-24,extended-cache-ttl-2025-04-11,cache-diagnosis-2026-04-07"

func genuineServerPolicyBody(model string) []byte {
	return []byte(`{"model":"` + model + `","messages":[{"role":"user","content":[{"type":"text","text":"<system-reminder>Today’s date is 2026/07/30.</system-reminder>"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"old","signature":"old-signature"}]}],"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.214.abc; cc_entrypoint=cli;"},{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude."}],"metadata":{"user_id":"{\"device_id\":\"downstream-device\",\"account_uuid\":\"downstream-account\",\"session_id\":\"` + identityPolicySession + `\"}"},"stream":true}`)
}

func genericServerPolicyBody(model string) []byte {
	return []byte(`{"model":"` + model + `","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"old","signature":"old-signature"}]}],"system":[{"type":"text","text":"ordinary API caller"}],"stream":true}`)
}

func rewritePolicyForBody(t *testing.T, body []byte) mimicry.RequestPolicy {
	t.Helper()
	class := mimicry.ClassifyClaudeCodeRequest(body)
	policy, err := mimicry.NewClaudeCodeRequestPolicy(class, mimicry.GenuineRequestRewrite)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestClientSlotIDRetainsLegacyHeaderAuthority(t *testing.T) {
	body := genuineServerPolicyBody("claude-sonnet-5")
	c, _ := newMessagesContext(t, body)
	c.Request.Header.Set("X-Claude-Code-Session-Id", "conflicting-header-session")
	if got := clientSlotID(c); got != "conflicting-header-session" {
		t.Fatalf("slot session: got %q want legacy header value", got)
	}
}

func TestPrepareClaudeMessagesBodyRewriteAlignsAccount(t *testing.T) {
	credential := oauthTestCred()
	credential.SetModelMap(map[string]string{"claude-opus-4-7": "claude-opus-4-8"})
	body := genuineServerPolicyBody("claude-opus-4-7")
	policy := rewritePolicyForBody(t, body)
	id := mimicry.SimIdentity{AccountKey: credential.AccountKey(), AccountUUID: credential.AccountUUIDValue(), ClientToken: "client-token"}
	prepared, err := prepareClaudeRewriteBody(body, "claude-opus-4-7", credential, id, policy)
	if err != nil {
		t.Fatal(err)
	}
	out := string(prepared.Body())
	if !strings.Contains(out, "Today's date is 2026-07-30") {
		t.Fatalf("rewrite did not normalize dateline: %s", out)
	}
	if !strings.Contains(out, `"model":"claude-opus-4-8"`) {
		t.Fatalf("rewrite did not apply selected-account model policy: %s", out)
	}
	if !strings.Contains(out, credential.AccountUUIDValue()) || strings.Contains(out, "downstream-account") {
		t.Fatalf("rewrite did not align metadata to the selected account: %s", out)
	}
}

func TestClaudeRewriteAuditMapsAnonymizedAccount(t *testing.T) {
	credential := oauthTestCred()
	body := genuineServerPolicyBody("claude-opus-5")
	policy := rewritePolicyForBody(t, body)
	id := mimicry.SimIdentity{
		AccountKey: credential.AccountKey(), AccountUUID: credential.AccountUUIDValue(), ClientToken: "client-token",
	}
	prepared, err := prepareClaudeRewriteBody(body, "claude-opus-5", credential, id, policy)
	if err != nil {
		t.Fatal(err)
	}
	audit := claudeIdentityAudit(prepared, credential.AccountKey())
	if audit == nil {
		t.Fatal("missing Claude rewrite audit")
	}
	if audit.AccountHash == "" || audit.AccountHash == credential.AccountKey() {
		t.Fatalf("account identity was not anonymized: %q", audit.AccountHash)
	}
	if !audit.AccountIdentityMapped || audit.IdentityMode != "rewrite" {
		t.Fatalf("unexpected audit flags: %+v", audit)
	}
}

func TestRewriteModelFieldPreservingBytesChangesOnlyModel(t *testing.T) {
	body := []byte("{\n  \"metadata\": {\"literal\":\"<>&\\u2028\"},\n  \"model\" : \"claude-opus-4-7\",\n  \"messages\": [ { \"role\": \"user\", \"content\": \"\u4e2d文\" } ]\n}\n")
	want := bytes.Replace(body, []byte(`"claude-opus-4-7"`), []byte(`"vendor/claude-opus-4-8"`), 1)
	original := bytes.Clone(body)

	got, err := rewriteModelFieldPreservingBytes(body, "vendor/claude-opus-4-8")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("model rewrite changed unrelated bytes\nwant: %q\n got: %q", want, got)
	}
	if !bytes.Equal(body, original) {
		t.Fatal("model rewrite mutated its input")
	}
}

func TestRewriteModelFieldPreservingBytesRejectsDuplicateModel(t *testing.T) {
	if _, err := rewriteModelFieldPreservingBytes([]byte(`{"model":"a","model":"b"}`), "c"); err == nil {
		t.Fatal("duplicate top-level model was accepted")
	}
}

func TestDoForwardGenericRequestUsesEstablishedBodyHeadersAndSignatureRetry(t *testing.T) {
	body := genericServerPolicyBody("claude-sonnet-5")
	var calls atomic.Int32
	type capturedRequest struct {
		body    []byte
		accept  string
		session string
	}
	var captured []capturedRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		upstreamBody, _ := io.ReadAll(r.Body)
		captured = append(captured, capturedRequest{
			body:    upstreamBody,
			accept:  r.Header.Get("Accept"),
			session: r.Header.Get("X-Claude-Code-Session-Id"),
		})
		w.WriteHeader(http.StatusBadRequest)
		if len(captured) == 1 {
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"Invalid signature in thinking block"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"still invalid"}}`))
	}))
	defer upstream.Close()

	credential := oauthTestCred()
	s := newDoForwardTestServer(t, upstream.URL, credential)
	c, recorder := newMessagesContext(t, body)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.214 (external, cli)")
	c.Request.Header.Set("Accept", "application/json")
	c.Request.Header.Set("X-Claude-Code-Session-Id", "legacy-header-session")

	retry, done, _ := s.doForward(c, credential, "/v1/messages", body, true,
		"claude-sonnet-5", "tok-abcdef123456", "legacy-header-session", "client", time.Now(), 1, false)
	if retry || !done || recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected result: retry=%v done=%v status=%d", retry, done, recorder.Code)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("generic signature recovery sent %d upstream calls, want 2", got)
	}
	id := mimicry.SimIdentity{
		AccountKey: credential.AccountKey(), AccountUUID: credential.AccountUUIDValue(), ClientToken: "tok-abcdef123456",
	}
	wantFirst := mimicry.ApplyClaudeCodeBodyMimicry(body, "claude-sonnet-5", id)
	if normalized, changed := mimicry.NormalizeDateline(wantFirst); changed {
		wantFirst = normalized
	}
	wantRetry := thinkingsig.SanitizeForSwitch(body)
	wantRetry = mimicry.ApplyClaudeCodeBodyMimicry(wantRetry, "claude-sonnet-5", id)
	if normalized, changed := mimicry.NormalizeDateline(wantRetry); changed {
		wantRetry = normalized
	}
	if !bytes.Equal(captured[0].body, wantFirst) {
		t.Fatalf("generic first request diverged from established body pipeline\nwant: %s\n got: %s", wantFirst, captured[0].body)
	}
	if !bytes.Equal(captured[1].body, wantRetry) {
		t.Fatalf("generic retry diverged from established body pipeline\nwant: %s\n got: %s", wantRetry, captured[1].body)
	}
	for i, request := range captured {
		if request.accept != "text/event-stream" {
			t.Fatalf("request %d Accept = %q, want legacy text/event-stream", i+1, request.accept)
		}
		if request.session != "legacy-header-session" {
			t.Fatalf("request %d session = %q, want legacy ingress header", i+1, request.session)
		}
	}
}

func TestDoForwardRewriteRejectsMissingBetaBeforeUpstream(t *testing.T) {
	body := genuineServerPolicyBody("claude-sonnet-5")
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	credential := oauthTestCred()
	s := newDoForwardTestServer(t, upstream.URL, credential)
	c, recorder := newMessagesContext(t, body)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.214 (external, cli)")
	c.Request.Header.Set("Accept", "application/json")

	retry, done, _ := s.doForward(c, credential, "/v1/messages", body, true,
		"claude-sonnet-5", "tok-abcdef123456", identityPolicySession, "client", time.Now(), 1, false)
	if retry || !done || recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected result: retry=%v done=%v status=%d", retry, done, recorder.Code)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("missing beta reached upstream %d times", got)
	}
}

func TestDoForwardGenuineRequestRejectsMissingAccountUUIDBeforeUpstream(t *testing.T) {
	body := genuineServerPolicyBody("claude-sonnet-5")
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	credential := oauthTestCred()
	credential.AccountUUID = ""
	s := newDoForwardTestServer(t, upstream.URL, credential)
	c, recorder := newMessagesContext(t, body)
	c.Request.Header.Set("Anthropic-Beta", identityPolicyMainBeta)

	retry, done, _ := s.doForward(c, credential, "/v1/messages", body, true,
		"claude-sonnet-5", "tok-abcdef123456", identityPolicySession, "client", time.Now(), 1, false)
	if retry || !done || recorder.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected result: retry=%v done=%v status=%d", retry, done, recorder.Code)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("missing account UUID reached upstream %d times", got)
	}
}

func TestDoForwardRewriteFailureCarriesModeAndAccountAudit(t *testing.T) {
	body := genuineServerPolicyBody("claude-sonnet-5")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Request-Id", "req-failed-response")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"type":"error","error":{"message":"organization has been disabled"}}`))
	}))
	defer upstream.Close()

	credential := oauthTestCred()
	s := newDoForwardTestServer(t, upstream.URL, credential)
	c, _ := newMessagesContext(t, body)
	c.Request.Header.Set("Anthropic-Beta", identityPolicyMainBeta)

	retry, done, deferred := s.doForward(c, credential, "/v1/messages", body, true,
		"claude-sonnet-5", "tok-abcdef123456", identityPolicySession, "client", time.Now(), 1, false)
	if !retry || done || deferred == nil || deferred.claudeAudit == nil {
		t.Fatalf("missing deferred failure audit: retry=%v done=%v deferred=%+v", retry, done, deferred)
	}
	audit := deferred.claudeAudit
	if audit.IdentityMode != "rewrite" || audit.AccountHash == "" || audit.AccountHash == credential.AccountKey() ||
		!audit.AccountIdentityMapped || !audit.CredentialHardFailed {
		t.Fatalf("incomplete failure audit: %+v", audit)
	}
	if !credential.IsHardFailed() {
		t.Fatal("ban did not hard-fail credential")
	}
}

func TestDoForwardThinkingSwitchTracksRealAccountNotCredentialFile(t *testing.T) {
	body := genuineServerPolicyBody("claude-sonnet-5")
	var captured [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, _ := io.ReadAll(r.Body)
		captured = append(captured, requestBody)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"ordinary invalid request"}}`))
	}))
	defer upstream.Close()

	credentialA := oauthTestCred()
	credentialB := oauthTestCred()
	credentialB.ID = "different-file-for-same-account"
	credentialB.AccessToken = "second-token"
	s := newDoForwardTestServer(t, upstream.URL, credentialA)
	for _, credential := range []*auth.Auth{credentialA, credentialB} {
		c, _ := newMessagesContext(t, body)
		c.Request.Header.Set("Accept", "application/json")
		c.Request.Header.Set("Anthropic-Beta", identityPolicyMainBeta)
		_, done, _ := s.doForward(c, credential, "/v1/messages", body, true,
			"claude-sonnet-5", "tok-abcdef123456", identityPolicySession, "client", time.Now(), 1, false)
		if !done {
			t.Fatal("request did not finish")
		}
	}
	if len(captured) != 2 || !strings.Contains(string(captured[1]), "old-signature") {
		t.Fatalf("same Anthropic account was treated as a switch: %q", captured)
	}

	// Replacing the same local filename with a different Anthropic account is
	// the inverse edge: it must sanitize even though the credential ID matches.
	captured = nil
	replacement := oauthTestCred()
	replacement.AccountUUID = "22222222-2222-2222-2222-222222222222"
	s.switchTracker = thinkingsig.NewSwitchTracker()
	for _, credential := range []*auth.Auth{credentialA, replacement} {
		c, _ := newMessagesContext(t, body)
		c.Request.Header.Set("Accept", "application/json")
		c.Request.Header.Set("Anthropic-Beta", identityPolicyMainBeta)
		_, done, _ := s.doForward(c, credential, "/v1/messages", body, true,
			"claude-sonnet-5", "tok-abcdef123456", identityPolicySession, "client", time.Now(), 1, false)
		if !done {
			t.Fatal("request did not finish")
		}
	}
	if len(captured) != 2 || strings.Contains(string(captured[1]), "old-signature") {
		t.Fatalf("different Anthropic account was not treated as a switch: %q", captured)
	}
}

func TestDoForwardGenericRequestRetainsCredentialFileSwitchKey(t *testing.T) {
	body := genericServerPolicyBody("claude-sonnet-5")
	var captured [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, _ := io.ReadAll(r.Body)
		captured = append(captured, requestBody)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"ordinary invalid request"}}`))
	}))
	defer upstream.Close()

	credentialA := oauthTestCred()
	credentialB := oauthTestCred()
	credentialB.ID = "different-file-for-same-account"
	credentialB.AccessToken = "second-token"
	s := newDoForwardTestServer(t, upstream.URL, credentialA)

	for _, credential := range []*auth.Auth{credentialA, credentialB} {
		c, _ := newMessagesContext(t, body)
		c.Request.Header.Set("X-Claude-Code-Session-Id", identityPolicySession)
		_, done, _ := s.doForward(c, credential, "/v1/messages", body, true,
			"claude-sonnet-5", "tok-abcdef123456", identityPolicySession, "client", time.Now(), 1, false)
		if !done {
			t.Fatal("request did not finish")
		}
	}
	if len(captured) != 2 {
		t.Fatalf("captured %d requests, want 2", len(captured))
	}
	if !strings.Contains(string(captured[0]), "old-signature") {
		t.Fatal("first generic request was unexpectedly sanitized")
	}
	if strings.Contains(string(captured[1]), "old-signature") {
		t.Fatal("generic request stopped using the credential-file switch key")
	}
}

func TestDoForwardRewriteRepreparesSignatureRetry(t *testing.T) {
	body := genuineServerPolicyBody("claude-sonnet-5")
	type capturedRequest struct {
		body    []byte
		session string
		accept  string
		ua      string
		beta    string
	}
	var captured []capturedRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, _ := io.ReadAll(r.Body)
		captured = append(captured, capturedRequest{
			body: requestBody, session: r.Header.Get("X-Claude-Code-Session-Id"),
			accept: r.Header.Get("Accept"), ua: r.Header.Get("User-Agent"), beta: r.Header.Get("Anthropic-Beta"),
		})
		w.WriteHeader(http.StatusBadRequest)
		if len(captured) == 1 {
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"Invalid signature in thinking block"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"still invalid"}}`))
	}))
	defer upstream.Close()

	credential := oauthTestCred()
	s := newDoForwardTestServer(t, upstream.URL, credential)
	c, recorder := newMessagesContext(t, body)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.214 (external, cli)")
	c.Request.Header.Set("Accept", "application/json")
	c.Request.Header.Set("Anthropic-Beta", identityPolicyMainBeta)
	c.Request.Header.Set("X-Claude-Code-Session-Id", identityPolicySession)

	retry, done, _ := s.doForward(c, credential, "/v1/messages", body, true,
		"claude-sonnet-5", "tok-abcdef123456", identityPolicySession, "client", time.Now(), 1, false)
	if retry || !done || recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected result: retry=%v done=%v status=%d", retry, done, recorder.Code)
	}
	if len(captured) != 2 {
		t.Fatalf("signature recovery sent %d requests, want 2", len(captured))
	}
	for i, request := range captured {
		out := string(request.body)
		if strings.Contains(out, "downstream-account") {
			t.Fatalf("request %d retained downstream account: %s", i+1, out)
		}
		if !strings.Contains(out, credential.AccountUUIDValue()) {
			t.Fatalf("request %d missing selected account UUID: %s", i+1, out)
		}
		if request.accept != "application/json" || request.ua != mimicry.ClaudeCLIUserAgent {
			t.Fatalf("request %d profile: accept=%q ua=%q", i+1, request.accept, request.ua)
		}
		if request.beta != identityPolicyMainBeta {
			t.Fatalf("request %d beta vector changed: %q", i+1, request.beta)
		}
		var envelope struct {
			Metadata struct {
				UserID string `json:"user_id"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(request.body, &envelope); err != nil {
			t.Fatal(err)
		}
		var identity struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(envelope.Metadata.UserID), &identity); err != nil {
			t.Fatal(err)
		}
		if request.session == "" || request.session != identity.SessionID {
			t.Fatalf("request %d header/body session mismatch: header=%q body=%q", i+1, request.session, identity.SessionID)
		}
	}
	if !strings.Contains(string(captured[0].body), "old-signature") {
		t.Fatal("first request was unexpectedly pre-sanitized")
	}
	if strings.Contains(string(captured[1].body), "old-signature") {
		t.Fatal("signature retry did not sanitize prior thinking")
	}
}
