package server

import (
	"strings"
	"testing"

	"github.com/wjsoj/cc-core/codexws"
	"github.com/wjsoj/cc-core/downstream"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/usage"
)

// testCodexIdent is a server-derived upstream identity, the kind the acquire
// loop mints. It is deliberately NOT the downstream slot id: the session id
// becomes our upstream prompt_cache_key, so it must never be a value a client
// can choose.
func testCodexIdent() mimicry.CodexFrameIdentity {
	return codexws.NewSessionRegistry(0).Identity("acct-key", "acct-key|sk-tok|slot")
}

// A trusted relay's slot id is "clienthash/session" — it has a slash and is not
// a UUID. Feeding it straight to the frame rewriter would fail validation, which
// is exactly why the session id is derived server-side from the slot rather than
// being the slot. This pins that the derived id IS acceptable.
func TestCodexWSIdentityDerivedFromNonUUIDSlot(t *testing.T) {
	reg := codexws.NewSessionRegistry(0)
	ident := reg.Identity("acct-key", "acct-key|sk-tok|3f2a9c1d4b5e6f70/my-window")
	frame := []byte(`{"type":"response.create","model":"gpt-5-codex","input":[]}`)
	out, err := mimicry.RewriteCodexClientFrame(frame, ident)
	if err != nil {
		t.Fatalf("a derived identity must always be rewritable, got %v", err)
	}
	if !strings.Contains(string(out), `"client_metadata"`) {
		t.Errorf("rewritten frame lacks client_metadata: %s", out)
	}
	// Stable across calls: the session id is our prompt_cache_key, and a fresh
	// one per frame would miss the upstream cache on every turn.
	again := reg.Identity("acct-key", "acct-key|sk-tok|3f2a9c1d4b5e6f70/my-window")
	if again.SessionID != ident.SessionID {
		t.Errorf("session id drifted: %q then %q", ident.SessionID, again.SessionID)
	}
}

// The rewrite is a LOCAL judgement. A frame it cannot handle must be forwarded
// untouched — never dropped, never a credential failure.
//
// The two failure modes are independent and must be pinned separately. Passing a
// bad frame AND a zero identity at once proves neither: Normalized() rejects the
// identity on the first line and returns before the frame is ever looked at, so
// such a case passes even with a perfectly good frame.

// Bad frame, GOOD identity — the frame itself is what the rewriter cannot handle.
func TestRebindCodexFrameForwardsUnhandleableFrame(t *testing.T) {
	ident := testCodexIdent()
	for _, frame := range []string{
		// Not JSON at all.
		`not json at all`,
		// A response.create whose object never closes: the append path has no
		// '}' to splice before and errors out.
		`{"type":"response.create","model":"gpt-5-codex"`,
	} {
		got := rebindCodexFrame([]byte(frame), ident, nil)
		if string(got) != frame {
			t.Errorf("a rebind failure must forward the original frame, got %q want %q", got, frame)
		}
	}
}

// GOOD frame, zero identity — the identity is what is unusable. This is the case
// a credential switch could produce, and the frame must still go through as-is
// rather than the turn dying.
func TestRebindCodexFrameForwardsOnUnusableIdentity(t *testing.T) {
	frame := []byte(`{"type":"response.create","model":"gpt-5-codex","input":[]}`)
	// Sanity: with a real identity this same frame IS rewritten, so the
	// pass-through below is the identity's doing and not a no-op frame.
	if rewritten := rebindCodexFrame(frame, testCodexIdent(), nil); string(rewritten) == string(frame) {
		t.Fatal("fixture frame is not rewritable even with a good identity — the test would prove nothing")
	}
	if got := rebindCodexFrame(frame, mimicry.CodexFrameIdentity{}, nil); string(got) != string(frame) {
		t.Errorf("an unusable identity must forward the original frame, got %q", got)
	}
}

// The prev-id strip runs before the rewrite, and neither may disturb the other:
// the strip must still remove the key, and the rewrite must not resurrect it or
// break model extraction that billing depends on.
func TestStripThenRebindKeepsFrameReadable(t *testing.T) {
	frame := []byte(`{"type":"response.create","model":"gpt-5-codex","previous_response_id":"resp_abc","input":[]}`)
	stripped := removeCodexPreviousResponseID(frame)
	if codexPreviousResponseID(stripped) != "" {
		t.Fatalf("previous_response_id survived the strip: %s", stripped)
	}
	out := rebindCodexFrame(stripped, testCodexIdent(), nil)
	if codexPreviousResponseID(out) != "" {
		t.Errorf("rebind resurrected previous_response_id: %s", out)
	}
	if got := codexWSExtractModel(out); got != "gpt-5-codex" {
		t.Errorf("model after rebind = %q, want gpt-5-codex — billing reads this", got)
	}
}

// Scrubbing is applied to `out` only, after settlement reads `data`. This pins
// the invariant that makes that safe: the scrubbed frame still yields the exact
// same usage counts, so a future refactor that reads the scrubbed bytes cannot
// silently under-bill.
func TestScrubCodexEventPreservesUsage(t *testing.T) {
	data := []byte(`{"type":"response.completed","response":{"id":"resp_1","prompt_cache_key":"leak","service_tier":"priority","usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":40},"output_tokens":7}}}`)
	want := extractCodexBackendUsageFromJSON(data)
	if want.InputTokens == 0 && want.OutputTokens == 0 {
		t.Fatal("fixture parsed to zero usage — the test would prove nothing")
	}
	out, keep := downstream.ScrubCodexEvent(data)
	if !keep {
		t.Fatal("a terminal event must never be dropped — it carries the turn's billing")
	}
	if strings.Contains(string(out), "prompt_cache_key") {
		t.Errorf("prompt_cache_key leaked to the client: %s", out)
	}
	if got := extractCodexBackendUsageFromJSON(out); got != want {
		t.Errorf("usage changed across scrub: %+v want %+v", got, want)
	}
	if codexResponseID(out) != "resp_1" {
		t.Error("response.id must survive — it anchors the account binding")
	}
	if !codexTerminalEvent(out) {
		t.Error("terminal detection must survive the scrub")
	}
}

// The frames the scrub rewrites or drops must all be invisible to billing, so
// neither treatment can skip a settlement.
//
// Note the two treatments are NOT the same, despite what an earlier name here
// claimed: codex.rate_limits is REWRITTEN (downstream/codex.go's
// scrubCodexRateLimits returns keep=true — the client still needs to learn it is
// throttled), while the two pure-telemetry frames are dropped whole.
func TestScrubbedCodexFramesCarryNoUsage(t *testing.T) {
	var zero usage.Counts
	cases := []struct {
		frame string
		keep  bool // true = rewritten and still forwarded, false = dropped whole
	}{
		{`{"type":"codex.rate_limits","plan_type":"pro","rate_limits":{"primary":{"used_percent":12}},"credits":{"balance":3}}`, true},
		{`{"type":"codex.response.metadata","headers":{"cf-ray":"abc"}}`, false},
		{`{"type":"responsesapi.websocket_timing","timing_metrics":{"ttft_ms":120}}`, false},
	}
	for _, tc := range cases {
		b := []byte(tc.frame)
		if c := extractCodexBackendUsageFromJSON(b); c != zero {
			t.Errorf("%s parsed to non-zero usage %+v — scrubbing it would lose a charge", tc.frame, c)
		}
		if codexTerminalEvent(b) {
			t.Errorf("%s must not be terminal", tc.frame)
		}
		out, keep := downstream.ScrubCodexEvent(b)
		if keep != tc.keep {
			t.Errorf("%s: keep=%v, want %v", tc.frame, keep, tc.keep)
		}
		if !keep {
			continue
		}
		// Kept means rewritten, never passed through: the pool's quota state
		// must not survive.
		for _, leak := range []string{"plan_type", "used_percent", "credits"} {
			if strings.Contains(string(out), leak) {
				t.Errorf("%q survived the rewrite of %s: %s", leak, tc.frame, out)
			}
		}
	}
}
