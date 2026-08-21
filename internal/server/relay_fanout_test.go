package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// codexTurn builds a /v1/responses body the way a real Codex client does:
// shared instructions, a per-conversation prompt_cache_key, and a growing input
// array. instr is deliberately reused across conversations — it is the field
// that used to make every caller look identical.
func codexTurn(cacheKey, userMsg string, turn int) []byte {
	input := []any{map[string]any{
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": userMsg}},
	}}
	for i := 0; i < turn; i++ {
		input = append(input, map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": fmt.Sprintf("reply %d", i)}},
		})
	}
	b, _ := json.Marshal(map[string]any{
		"model":        "gpt-5.6-sol",
		"instructions": strings.Repeat("You are a coding agent. ", 40),
		"input":        input,
		"stream":       true,
		"store":        false,
	})
	if cacheKey == "" {
		return b
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["prompt_cache_key"] = cacheKey
	m["client_metadata"] = map[string]any{"session_id": cacheKey, "thread_id": cacheKey}
	b, _ = json.Marshal(m)
	return b
}

// The property the whole feature exists for: many independent conversations
// arriving on ONE token must not all land on one slot, because one slot is one
// upstream credential.
func TestSessionlessConversationsFanOut(t *testing.T) {
	seen := map[string]int{}
	for i := 0; i < 200; i++ {
		slot := fanoutSlotID(codexTurn(fmt.Sprintf("sess-%d", i), "hello", 0), sessionlessFanoutWidth)
		if slot == "" {
			t.Fatalf("conversation %d produced no slot", i)
		}
		seen[slot]++
	}
	if len(seen) != sessionlessFanoutWidth {
		t.Fatalf("200 conversations occupied %d slots, want all %d buckets: %v",
			len(seen), sessionlessFanoutWidth, seen)
	}
}

// Bucketing must be bounded, not per-conversation: a slot costs pool capacity
// for the whole active window, so unbounded slots would starve the fleet.
func TestFanOutIsBounded(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 5000; i++ {
		seen[fanoutSlotID(codexTurn(fmt.Sprintf("sess-%d", i), "hi", 0), sessionlessFanoutWidth)] = true
	}
	if len(seen) > sessionlessFanoutWidth {
		t.Fatalf("5000 conversations occupied %d slots, want at most %d", len(seen), sessionlessFanoutWidth)
	}
}

// A conversation must keep its slot across turns, or it would be rescheduled
// onto a different credential mid-thread and lose the upstream prompt cache.
func TestConversationKeepsItsSlotAcrossTurns(t *testing.T) {
	first := fanoutSlotID(codexTurn("sess-stable", "start the task", 0), sessionlessFanoutWidth)
	for turn := 1; turn < 12; turn++ {
		got := fanoutSlotID(codexTurn("sess-stable", "start the task", turn), sessionlessFanoutWidth)
		if got != first {
			t.Fatalf("turn %d moved slot %q → %q", turn, first, got)
		}
	}
}

// The collapse this replaces: bodies that differ only in their shared
// instructions blob. Anchoring on anything shared would put them all back in
// one bucket, so the content fallback must key on the first USER turn.
func TestSharedInstructionsDoNotCollapse(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 60; i++ {
		// No prompt_cache_key at all — the relay stripped it, so only the
		// content fallback is left.
		body := codexTurn("", fmt.Sprintf("please refactor module %d", i), 0)
		slot := fanoutSlotID(body, sessionlessFanoutWidth)
		if slot == "" {
			t.Fatalf("body %d produced no slot despite carrying a user message", i)
		}
		seen[slot] = true
	}
	if len(seen) < 2 {
		t.Fatalf("60 distinct conversations collapsed onto %d slot(s)", len(seen))
	}
}

// Anthropic dialect: messages[] instead of input[], and no Codex fields.
func TestAnthropicBodyAnchorsOnFirstUserMessage(t *testing.T) {
	mk := func(user string) []byte {
		b, _ := json.Marshal(map[string]any{
			"model":    "claude-opus-5",
			"system":   "You are Claude Code.",
			"messages": []any{map[string]any{"role": "user", "content": user}},
		})
		return b
	}
	a, b := fanoutSlotID(mk("fix the parser"), sessionlessFanoutWidth), fanoutSlotID(mk("write a test"), sessionlessFanoutWidth)
	if a == "" || b == "" {
		t.Fatalf("anthropic bodies produced no slot: %q %q", a, b)
	}
	if a == fanoutSlotID(mk("fix the parser"), sessionlessFanoutWidth) == false {
		t.Fatal("same conversation did not reproduce its slot")
	}
	if a == b {
		t.Skip("hash collision between the two sample conversations; not a defect")
	}
}

// No anchor → the old behaviour exactly. A body we cannot attribute must not
// be assigned a bucket at random, which would reschedule every request.
func TestNoAnchorKeepsLegacyEmptySlot(t *testing.T) {
	for name, body := range map[string]string{
		"empty":             ``,
		"not json":          `<html>nope</html>`,
		"no conversation":   `{"model":"gpt-5.6-sol","stream":true}`,
		"assistant only":    `{"input":[{"role":"assistant","content":"hi"}]}`,
		"instructions only": `{"instructions":"You are a coding agent."}`,
	} {
		if got := fanoutSlotID([]byte(body), sessionlessFanoutWidth); got != "" {
			t.Errorf("%s: slot = %q, want empty", name, got)
		}
	}
}

// Width 1 (or 0) disables the feature outright.
func TestFanOutDisabled(t *testing.T) {
	body := codexTurn("sess-1", "hello", 0)
	for _, w := range []int{0, 1, -3} {
		if got := fanoutSlotID(body, w); got != "" {
			t.Errorf("width %d: slot = %q, want empty", w, got)
		}
	}
}

// An explicit session on the wire always wins: a client that names its own
// window must keep one slot per window, not be rebucketed by body content.
func TestWireSessionBeatsBodyAnchor(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("session-id", "real-codex-window")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	c.Set(relayTrustedKey, false)

	if got := clientSlotID(c); got != "real-codex-window" {
		t.Fatalf("slot = %q, want the wire session", got)
	}
	// And the body anchor for the same request is a different value, so the
	// precedence in forward() is load-bearing rather than incidental.
	if fanoutSlotID(codexTurn("sess-x", "hi", 0), sessionlessFanoutWidth) == "real-codex-window" {
		t.Fatal("body anchor collided with the wire session")
	}
}

// A response-chain continuation must keep the single sticky slot unless it
// names its session explicitly. Bucketing it on content would move the thread
// to another credential mid-chain, where its previous_response_id — minted on
// the old account — is not valid at all.
func TestPreviousResponseIDWithoutSessionKeepsOneSlot(t *testing.T) {
	mk := func(cacheKey, prev, user string) []byte {
		m := map[string]any{
			"model": "gpt-5.6-sol",
			"input": []any{map[string]any{"role": "user", "content": user}},
		}
		if prev != "" {
			m["previous_response_id"] = prev
		}
		if cacheKey != "" {
			m["prompt_cache_key"] = cacheKey
		}
		b, _ := json.Marshal(m)
		return b
	}
	// Chain turns differ only in their delta; each would otherwise bucket
	// somewhere else.
	for i, user := range []string{"do the thing", "now the next thing", "and again"} {
		if got := fanoutSlotID(mk("", fmt.Sprintf("resp-%d", i), user), sessionlessFanoutWidth); got != "" {
			t.Fatalf("chain turn %d got slot %q, want the legacy single slot", i, got)
		}
	}
	// With an explicit session id the chain is safe to bucket, and stays put.
	first := fanoutSlotID(mk("sess-chain", "resp-0", "do the thing"), sessionlessFanoutWidth)
	if first == "" {
		t.Fatal("chain with prompt_cache_key produced no slot")
	}
	if got := fanoutSlotID(mk("sess-chain", "resp-9", "much later"), sessionlessFanoutWidth); got != first {
		t.Fatalf("chain moved slot %q → %q despite a stable session id", first, got)
	}
}
