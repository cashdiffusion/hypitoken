package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wjsoj/cc-core/auth"
)

// newTestAuth returns a minimal OAuth Auth suitable for sidecar exercises.
func newTestAuth(id, email string) *auth.Auth {
	return &auth.Auth{
		ID:               id,
		Kind:             auth.KindOAuth,
		Provider:         auth.ProviderAnthropic,
		Email:            email,
		AccessToken:      "sk-ant-oat01-test-" + id,
		AccountUUID:      "acct-uuid-" + id,
		OrganizationUUID: "org-uuid-" + id,
	}
}

// recorder collects (path, UA, beta) per call so tests can assert which
// endpoints were hit and which client identity was claimed for each.
type recorder struct {
	mu    sync.Mutex
	calls []recordedCall
}

type recordedCall struct {
	path string
	ua   string
	beta string
	body []byte
}

func (r *recorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.calls = append(r.calls, recordedCall{
			path: req.URL.Path,
			ua:   req.Header.Get("User-Agent"),
			beta: req.Header.Get("Anthropic-Beta"),
		})
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

func (r *recorder) snapshot() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// batchCapturingHandler is like handler() but also stores the request body
// for /api/event_logging/v2/batch hits so tests can inspect event counts.
func (r *recorder) batchCapturingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body []byte
		if strings.HasSuffix(req.URL.Path, "/api/event_logging/v2/batch") {
			b, _ := io.ReadAll(req.Body)
			body = b
		}
		r.mu.Lock()
		r.calls = append(r.calls, recordedCall{
			path: req.URL.Path,
			ua:   req.Header.Get("User-Agent"),
			beta: req.Header.Get("Anthropic-Beta"),
			body: body,
		})
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

// firstBatchBody returns the body of the first event_logging batch hit.
func (r *recorder) firstBatchBody() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if strings.HasSuffix(c.path, "/api/event_logging/v2/batch") {
			return c.body
		}
	}
	return nil
}

// TestBootstrapFiresAllStepsWithCorrectUA asserts that one Notify triggers
// the full 9-step bootstrap, that each step hits the right endpoint and
// claims the right User-Agent / Anthropic-Beta. Real CC mixes Bun fetch,
// axios, claude-cli, and claude-code across these endpoints — getting any
// one wrong is its own fingerprint.
func TestBootstrapFiresAllStepsWithCorrectUA(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	mgr := newSidecarMgr(sidecarConfig{enabled: true, baseURL: srv.URL})
	defer mgr.Stop()
	mgr.httpClient = srv.Client()

	a := newTestAuth("auth-1", "alice@example.com")
	mgr.Notify(a, "client-A")

	// Bootstrap takes ~2.4s of real timing — wait a bit beyond that for
	// all steps to land but stop before the heartbeat (8s) kicks in.
	if !waitForCallCount(rec, 9, 5*time.Second) {
		t.Fatalf("expected at least 8 bootstrap calls, got %d after timeout", rec.count())
	}

	calls := rec.snapshot()
	// Drop anything that's a heartbeat ping (path ends with /v2/batch) so
	// this test stays focused on the bootstrap surface.
	bootstrapCalls := make([]recordedCall, 0, len(calls))
	for _, c := range calls {
		if strings.HasSuffix(c.path, "/api/event_logging/v2/batch") {
			continue
		}
		bootstrapCalls = append(bootstrapCalls, c)
	}

	// Expected (path → required UA fragment, beta).
	wants := map[string]struct {
		uaFragment string
		beta       string
	}{
		"/api/eval/sdk-zAZezfDKGoZuXXKe":  {"Bun/", "oauth-2025-04-20"},
		"/api/oauth/account/settings":    {"claude-code/", "oauth-2025-04-20"},
		"/api/claude_code_grove":         {"claude-code/", "oauth-2025-04-20"},
		"/api/claude_cli/bootstrap":      {"claude-code/", "oauth-2025-04-20"},
		"/api/claude_code_penguin_mode":  {"axios/", "oauth-2025-04-20"},
		"/v1/messages":                   {"claude-cli/", quotaProbeBeta},
		"/mcp-registry/v0/servers":       {"axios/", ""},
		"/v1/mcp_servers":                {"axios/", "mcp-servers-2025-12-04"},
		"/v1/code/triggers":              {"axios/", "ccr-triggers-2026-01-30"},
		// /claude-code-releases/latest is on a different host (downloads.claude.ai)
		// — won't hit our test server, so it's not in this list.
	}

	seen := make(map[string]bool)
	for _, c := range bootstrapCalls {
		want, ok := wants[c.path]
		if !ok {
			continue
		}
		seen[c.path] = true
		if !strings.HasPrefix(c.ua, want.uaFragment) {
			t.Errorf("path %s: UA must start with %q, got %q", c.path, want.uaFragment, c.ua)
		}
		if c.beta != want.beta {
			t.Errorf("path %s: beta must be %q, got %q", c.path, want.beta, c.beta)
		}
	}
	for path := range wants {
		if !seen[path] {
			t.Errorf("missing bootstrap step for %s", path)
		}
	}
}

// TestBootstrapFiresOncePerSession asserts re-entrant Notify calls don't
// re-fire bootstrap.
func TestBootstrapFiresOncePerSession(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	mgr := newSidecarMgr(sidecarConfig{enabled: true, baseURL: srv.URL})
	defer mgr.Stop()
	mgr.httpClient = srv.Client()

	a := newTestAuth("auth-1", "alice@example.com")
	mgr.Notify(a, "client-A")
	mgr.Notify(a, "client-A")
	mgr.Notify(a, "client-A")

	// Wait for bootstrap to finish.
	if !waitForCallCount(rec, 9, 5*time.Second) {
		t.Fatalf("bootstrap never completed, got %d calls", rec.count())
	}
	time.Sleep(300 * time.Millisecond)
	count := rec.count()

	// Filter out heartbeat batches (any /v2/batch hits) — they're not
	// part of "bootstrap", they're the steady-state ticker that may also
	// fire during the wait.
	bootstrapHits := 0
	for _, c := range rec.snapshot() {
		if !strings.HasSuffix(c.path, "/api/event_logging/v2/batch") {
			bootstrapHits++
		}
	}
	if bootstrapHits > 10 {
		t.Errorf("bootstrap should fire at most 9 endpoints (downloads is off-host) per session, got %d (total recorded %d)", bootstrapHits, count)
	}
}

// TestBootstrapDeduplicatesAcrossClientTokens asserts that two downstream
// client_tokens routing through the same OAuth account share ONE bootstrap.
// Upstream must see "one CLI process serving multiple chat sessions", not
// "this user launched the CLI twice in five seconds" — the latter is the
// fingerprint we got banned for. (Each chat still gets its own session_id
// from SessionIDFor; only the bootstrap/heartbeat sidecar collapses.)
func TestBootstrapDeduplicatesAcrossClientTokens(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	mgr := newSidecarMgr(sidecarConfig{enabled: true, baseURL: srv.URL})
	defer mgr.Stop()
	mgr.httpClient = srv.Client()

	a := newTestAuth("auth-1", "alice@example.com")
	mgr.Notify(a, "client-A")
	mgr.Notify(a, "client-B")

	// One bootstrap should fire ~8 on-host calls, regardless of token count.
	if !waitForCallCount(rec, 9, 5*time.Second) {
		t.Fatalf("expected ~8 bootstrap calls (one shared burst), got %d", rec.count())
	}
	// Wait a bit longer to confirm a second burst doesn't sneak in.
	time.Sleep(500 * time.Millisecond)
	bootstrapHits := 0
	for _, c := range rec.snapshot() {
		if !strings.HasSuffix(c.path, "/api/event_logging/v2/batch") {
			bootstrapHits++
		}
	}
	if bootstrapHits > 10 {
		t.Errorf("expected at most 9 bootstrap hits across both client_tokens (per-account dedupe), got %d", bootstrapHits)
	}
}

// TestSidecarSkipsAPIKey asserts API-key credentials don't trigger sidecars.
func TestSidecarSkipsAPIKey(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	mgr := newSidecarMgr(sidecarConfig{enabled: true, baseURL: srv.URL})
	defer mgr.Stop()
	mgr.httpClient = srv.Client()

	a := &auth.Auth{ID: "apikey:foo", Kind: auth.KindAPIKey, Provider: auth.ProviderAnthropic, AccessToken: "sk-test"}
	mgr.Notify(a, "client-A")
	time.Sleep(500 * time.Millisecond)
	if rec.count() != 0 {
		t.Errorf("apikey credentials must not trigger sidecars, got %d", rec.count())
	}
}

// TestSidecarDisabled asserts the kill switch works.
func TestSidecarDisabled(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	mgr := newSidecarMgr(sidecarConfig{enabled: false, baseURL: srv.URL})
	defer mgr.Stop()

	a := newTestAuth("auth-1", "alice@example.com")
	mgr.Notify(a, "client-A")
	time.Sleep(300 * time.Millisecond)
	if rec.count() != 0 {
		t.Errorf("disabled sidecar must never call upstream, got %d", rec.count())
	}
}

// TestSidecarRefiresAfterIdle: rewinds lastSeen so the manager treats the
// next Notify as a wake-up after a long idle (real user reopening claude).
func TestSidecarRefiresAfterIdle(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	mgr := newSidecarMgr(sidecarConfig{enabled: true, baseURL: srv.URL})
	defer mgr.Stop()
	mgr.httpClient = srv.Client()

	a := newTestAuth("auth-1", "alice@example.com")
	mgr.Notify(a, "client-A")
	if !waitForCallCount(rec, 9, 5*time.Second) {
		t.Fatalf("first bootstrap never landed, got %d calls", rec.count())
	}

	// Rewind lastSeen past the idle TTL AND lastBootstrap past the
	// cooldown so the next Notify treats it as a fresh boot. With only
	// the session-level rewind, the cooldown (default 12h) would keep
	// bootstrap suppressed, which is the production-correct behavior but
	// not what this test exercises.
	v, ok := mgr.sessions.Load(a.AccountKey())
	if !ok {
		t.Fatalf("session entry missing")
	}
	sess := v.(*sidecarSession)
	sess.lastSeen.Store(time.Now().Add(-2 * sidecarSessionIdleTTL).UnixNano())
	if av, ok := mgr.anchors.Load(a.AccountKey()); ok {
		av.(*accountAnchor).lastBootstrap.Store(time.Now().Add(-2 * bootstrapCooldown).UnixNano())
	}

	before := rec.count()
	mgr.Notify(a, "client-A")
	if !waitForCallCount(rec, before+9, 5*time.Second) {
		t.Errorf("expected another bootstrap burst after idle, got %d calls (before=%d)", rec.count(), before)
	}
}

// TestBootstrapCooldownSuppressesBurst: an account whose anchor records a
// recent bootstrap must NOT re-fire the burst on a fresh session, even
// after idle eviction. Heartbeat still starts so the simulated process
// continues to look alive.
func TestBootstrapCooldownSuppressesBurst(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	mgr := newSidecarMgr(sidecarConfig{enabled: true, baseURL: srv.URL})
	defer mgr.Stop()
	mgr.httpClient = srv.Client()

	a := newTestAuth("auth-1", "alice@example.com")
	// Pre-stamp a recent bootstrap on the anchor so Notify hits the
	// cooldown branch on its very first call.
	anchor := mgr.anchorFor(a.AccountKey())
	anchor.lastBootstrap.Store(time.Now().UnixNano())

	ready := mgr.Notify(a, "client-A")
	// bootstrapReady must be closed immediately so callers don't block on
	// a bootstrap that's never going to happen.
	select {
	case <-ready:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("bootstrapReady should be closed immediately under cooldown")
	}

	// Give bootstrap a chance to misfire if the cooldown failed. None of
	// the 9 endpoints should be hit; only the heartbeat at +8s+ may fire.
	time.Sleep(3 * time.Second)
	bootstrapHits := 0
	for _, c := range rec.snapshot() {
		if !strings.HasSuffix(c.path, "/api/event_logging/v2/batch") {
			bootstrapHits++
		}
	}
	if bootstrapHits != 0 {
		t.Errorf("cooldown should suppress bootstrap entirely, got %d non-heartbeat hits", bootstrapHits)
	}
}

// TestStartupBatchSuppressedUnderCooldown: when bootstrap is suppressed,
// the heartbeat's "first batch" must NOT be the fat ~80-event startup
// dump — that batch is a process-launch signal in real CC, and re-firing
// it on every wake-from-idle defeats the bootstrap cooldown.
func TestStartupBatchSuppressedUnderCooldown(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.batchCapturingHandler())
	defer srv.Close()

	mgr := newSidecarMgr(sidecarConfig{enabled: true, baseURL: srv.URL})
	defer mgr.Stop()
	mgr.httpClient = srv.Client()

	a := newTestAuth("auth-1", "alice@example.com")
	mgr.anchorFor(a.AccountKey()).lastBootstrap.Store(time.Now().UnixNano())
	mgr.Notify(a, "client-A")

	// Wait past the +8s heartbeat warmup so the first batch lands.
	deadline := time.Now().Add(15 * time.Second)
	var firstBatch []byte
	for time.Now().Before(deadline) {
		if b := rec.firstBatchBody(); b != nil {
			firstBatch = b
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if firstBatch == nil {
		t.Fatalf("no event_logging batch landed within 15s")
	}
	var parsed struct {
		Events []any `json:"events"`
	}
	if err := json.Unmarshal(firstBatch, &parsed); err != nil {
		t.Fatalf("parse batch: %v", err)
	}
	if len(parsed.Events) > 5 {
		t.Errorf("startup batch should be suppressed under cooldown; got %d events (expected ≤5 steady-state)", len(parsed.Events))
	}
}

// TestDatadogHeartbeatBodyShape verifies the Datadog payload matches the
// captured row 16/21 shape: a JSON ARRAY of one event with all the
// flattened fields, ddtags carrying the indexed dimensions, and
// user_bucket present at the top level (as int).
func TestDatadogHeartbeatBodyShape(t *testing.T) {
	a := newTestAuth("auth-1", "alice@example.com")
	body, err := buildDatadogHeartbeatBody(a, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	s := string(body)
	for _, want := range []string{
		`"ddsource":"nodejs"`,
		`"message":"tengu_dir_search"`,
		`"service":"claude-code"`,
		`"version":"` + CLICurrentVersion + `"`,
		`"arch":"` + claudeStainlessArch + `"`,
		`"subscription_type":"max"`,
		`"is_claude_ai_auth":true`,
		`"session_id":"00000000-0000-0000-0000-000000000000"`,
		`event:tengu_dir_search`,
		`subscription_type:max`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("datadog body missing %s", want)
		}
	}
	// Datadog expects an array.
	if s[0] != '[' {
		t.Errorf("datadog body must be a JSON array, got %q", s[0])
	}
}

// TestUserBucketStability asserts user_bucket is deterministic per
// account (so all heartbeats from one account land in the same Datadog
// slice) and varies across accounts.
func TestUserBucketStability(t *testing.T) {
	b1 := userBucketFor("alice@example.com")
	b1again := userBucketFor("alice@example.com")
	if b1 != b1again {
		t.Errorf("user_bucket must be stable per account: %d vs %d", b1, b1again)
	}
	if b1 < 0 || b1 > 99 {
		t.Errorf("user_bucket out of [0,99]: %d", b1)
	}
	// Different account → different bucket (with overwhelming probability).
	b2 := userBucketFor("bob@example.com")
	if b1 == b2 {
		t.Logf("warning: alice and bob hashed to the same bucket %d (1%% probability, acceptable)", b1)
	}
}

// TestHeartbeatBodyShape asserts the buildHeartbeatBody output matches
// the structural invariants Anthropic's intake validates: events array
// with one ClaudeCodeInternalEvent, env block carrying our pinned
// fingerprint, account_uuid + device_id + email present.
func TestHeartbeatBodyShape(t *testing.T) {
	a := newTestAuth("auth-1", "alice@example.com")
	body, err := buildHeartbeatBody(a, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	s := string(body)
	for _, want := range []string{
		`"event_type":"ClaudeCodeInternalEvent"`,
		`"event_name":"tengu_dir_search"`,
		`"version":"` + CLICurrentVersion + `"`,
		`"arch":"` + claudeStainlessArch + `"`,
		`"node_version":"` + claudeStainlessRuntimeV + `"`,
		`"account_uuid":"acct-uuid-auth-1"`,
		`"organization_uuid":"org-uuid-auth-1"`,
		`"email":"alice@example.com"`,
		`"session_id":"00000000-0000-0000-0000-000000000000"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("heartbeat body missing %s\n%s", want, s)
		}
	}
}

// TestStartupHeartbeatBatchShape asserts the fat first-launch event_logging
// batch matches the captured row 14 invariants: ~80 events, multiple
// distinct event_names with the right relative volume (skill_loaded
// dominant, plugin_enabled secondary, plus singletons), shared session_id
// and identity across every event.
func TestStartupHeartbeatBatchShape(t *testing.T) {
	a := newTestAuth("auth-1", "alice@example.com")
	body, err := buildStartupHeartbeatBody(a, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	var parsed struct {
		Events []struct {
			EventType string `json:"event_type"`
			EventData struct {
				EventName string `json:"event_name"`
				SessionID string `json:"session_id"`
				EventID   string `json:"event_id"`
				Auth      struct {
					AccountUUID string `json:"account_uuid"`
				} `json:"auth"`
			} `json:"event_data"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v\n%s", err, body)
	}
	if n := len(parsed.Events); n < 60 || n > 100 {
		t.Errorf("startup batch should be ~80 events (close to row-14's 99), got %d", n)
	}
	counts := map[string]int{}
	seenIDs := map[string]bool{}
	for _, e := range parsed.Events {
		if e.EventType != "ClaudeCodeInternalEvent" {
			t.Errorf("wrong event_type %q", e.EventType)
		}
		if e.EventData.SessionID != "00000000-0000-0000-0000-000000000000" {
			t.Errorf("event missing/wrong session_id: %q", e.EventData.SessionID)
		}
		if e.EventData.Auth.AccountUUID != "acct-uuid-auth-1" {
			t.Errorf("event missing account_uuid")
		}
		if e.EventData.EventID == "" {
			t.Errorf("event missing event_id")
		}
		if seenIDs[e.EventData.EventID] {
			t.Errorf("duplicate event_id %s — every event should have a unique id", e.EventData.EventID)
		}
		seenIDs[e.EventData.EventID] = true
		counts[e.EventData.EventName]++
	}
	// Distribution sanity: skill_loaded dominates, plugin_enabled
	// secondary, plus a long tail of singletons.
	if counts["tengu_skill_loaded"] < 20 {
		t.Errorf("expected tengu_skill_loaded to dominate (>=20), got %d", counts["tengu_skill_loaded"])
	}
	if len(counts) < 15 {
		t.Errorf("expected at least 15 distinct event_names in startup batch, got %d", len(counts))
	}
}

// TestSubscriptionAttrsFor verifies the mapping from cached
// organization_type / organization_rate_limit_tier to GrowthBook
// attribute values. claude_<tier> → <tier> matches what real CC sends
// in row 01 (organization_type=claude_max, subscriptionType=max).
func TestSubscriptionAttrsFor(t *testing.T) {
	cases := []struct {
		name, orgType, rlTier  string
		wantSubType, wantRLTier string
	}{
		{"empty → defaults", "", "", "max", "default_claude_max_20x"},
		{"claude_max", "claude_max", "default_claude_max_20x", "max", "default_claude_max_20x"},
		{"claude_pro", "claude_pro", "default_claude_pro_5x", "pro", "default_claude_pro_5x"},
		{"claude_team", "claude_team", "default_claude_team", "team", "default_claude_team"},
		{"non-prefixed value preserved", "enterprise", "custom_tier", "enterprise", "custom_tier"},
		{"rate-limit only", "", "default_claude_max_20x", "max", "default_claude_max_20x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAuth("auth-1", "alice@example.com")
			a.OrganizationType = tc.orgType
			a.OrganizationRateLimitTier = tc.rlTier
			gotSub, gotRL := subscriptionAttrsFor(a)
			if gotSub != tc.wantSubType {
				t.Errorf("subscriptionType: got %q want %q", gotSub, tc.wantSubType)
			}
			if gotRL != tc.wantRLTier {
				t.Errorf("rateLimitTier: got %q want %q", gotRL, tc.wantRLTier)
			}
		})
	}
}

// TestHandleBootstrapResponse verifies parsing the captured row-04 shape
// caches the right values onto the auth.
func TestHandleBootstrapResponse(t *testing.T) {
	a := newTestAuth("auth-1", "alice@example.com")
	body := []byte(`{
		"oauth_account": {
			"account_uuid": "00000000-0000-0000-0000-000000000001",
			"organization_type": "claude_pro",
			"organization_rate_limit_tier": "default_claude_pro_5x"
		}
	}`)
	handleBootstrapResponse(a, body)
	if a.OrganizationType != "claude_pro" {
		t.Errorf("OrganizationType = %q, want claude_pro", a.OrganizationType)
	}
	if a.OrganizationRateLimitTier != "default_claude_pro_5x" {
		t.Errorf("OrganizationRateLimitTier = %q, want default_claude_pro_5x", a.OrganizationRateLimitTier)
	}
	// Subsequent GrowthBook call now uses real values.
	subType, rlTier := subscriptionAttrsFor(a)
	if subType != "pro" || rlTier != "default_claude_pro_5x" {
		t.Errorf("after bootstrap: subType=%q rlTier=%q", subType, rlTier)
	}
}

// TestHandleBootstrapResponseEmpty asserts a payload with no usable
// fields leaves the cached values untouched (no spurious overwrite to
// empty strings).
func TestHandleBootstrapResponseEmpty(t *testing.T) {
	a := newTestAuth("auth-1", "alice@example.com")
	a.OrganizationType = "claude_max"
	a.OrganizationRateLimitTier = "default_claude_max_20x"
	handleBootstrapResponse(a, []byte(`{"oauth_account":{}}`))
	if a.OrganizationType != "claude_max" {
		t.Errorf("OrganizationType clobbered: %q", a.OrganizationType)
	}
}

func waitForCallCount(r *recorder, want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if r.count() >= want {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return r.count() >= want
}

// silence unused import warning when building before tests
var _ = atomic.Int32{}
