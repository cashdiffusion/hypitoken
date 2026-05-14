package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func testID() SimIdentity {
	return SimIdentity{
		AccountKey:  "test-account@example.com",
		AccountUUID: "00000000-0000-0000-0000-000000000000",
		ClientToken: "test-client-token",
	}
}

// TestMimicrySmoke exercises the body rewriter on the shapes /v1/messages
// requests actually take. It doesn't assert byte-equality against a golden
// payload (the cch hash and session UUID change with body content) — it
// asserts the structural invariants real Claude Code 2.1.141 requests carry.
func TestMimicrySmoke(t *testing.T) {
	cases := []struct {
		name              string
		in                string
		expectSystemCount int // 2 = no original system, 3 = single-block original, etc.
	}{
		{
			name:              "string-system",
			in:                `{"model":"claude-sonnet-4-5","system":"You are a helpful assistant.","messages":[{"role":"user","content":"hello"}]}`,
			expectSystemCount: 3,
		},
		{
			name:              "array-system",
			in:                `{"model":"claude-sonnet-4-5","system":[{"type":"text","text":"You are a helpful assistant."}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`,
			expectSystemCount: 3,
		},
		{
			name:              "no-system",
			in:                `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`,
			expectSystemCount: 2,
		},
		{
			name:              "multi-turn",
			in:                `{"model":"claude-sonnet-4-5","system":"sys","messages":[{"role":"user","content":"q1"},{"role":"assistant","content":"a1"},{"role":"user","content":"q2"},{"role":"assistant","content":"a2"},{"role":"user","content":"q3"}]}`,
			expectSystemCount: 3,
		},
		{
			name:              "two-block-system",
			in:                `{"model":"claude-sonnet-4-5","system":[{"type":"text","text":"part one"},{"type":"text","text":"part two"}],"messages":[{"role":"user","content":"hi"}]}`,
			expectSystemCount: 4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := applyClaudeCodeBodyMimicry([]byte(tc.in), "claude-sonnet-4-5", testID())

			// 1. Output must still be valid JSON.
			var parsed map[string]json.RawMessage
			if err := json.Unmarshal(out, &parsed); err != nil {
				t.Fatalf("output not valid JSON: %v\n%s", err, out)
			}

			// 2. system must be the right number of blocks for CC 2.1.141:
			//    [billing, "You are Claude Code...", ...originalBlocks].
			var sys []map[string]any
			if err := json.Unmarshal(parsed["system"], &sys); err != nil {
				t.Fatalf("system not array: %v", err)
			}
			if len(sys) != tc.expectSystemCount {
				t.Fatalf("expected %d system blocks, got %d", tc.expectSystemCount, len(sys))
			}
			billing, _ := sys[0]["text"].(string)
			if !strings.HasPrefix(billing, "x-anthropic-billing-header:") {
				t.Errorf("system[0] missing billing prefix: %q", billing)
			}
			if !strings.Contains(billing, "cc_version=2.1.141.") {
				t.Errorf("system[0] missing cc_version=2.1.141: %q", billing)
			}
			if strings.Contains(billing, "cch=00000") {
				t.Errorf("system[0] cch placeholder still present: %q", billing)
			}
			if _, hasCC := sys[0]["cache_control"]; hasCC {
				t.Errorf("system[0] (billing) must NOT carry cache_control")
			}

			ccPrompt, _ := sys[1]["text"].(string)
			if !strings.HasPrefix(ccPrompt, "You are Claude Code") {
				t.Errorf("system[1] missing CC prompt: %q", ccPrompt)
			}
			if _, hasCC := sys[1]["cache_control"]; hasCC {
				t.Errorf("system[1] (CC intro) must NOT carry cache_control — real 2.1.141 leaves it bare")
			}

			// 3. When original system was supplied, the LAST system block must
			//    carry cache_control with ttl=1h scope=global, matching real
			//    CC 2.1.141.
			if tc.expectSystemCount > 2 {
				last := sys[len(sys)-1]
				cc, ok := last["cache_control"].(map[string]any)
				if !ok {
					t.Fatalf("last system block missing cache_control")
				}
				if cc["ttl"] != "1h" {
					t.Errorf("last system cache_control ttl: want 1h got %v", cc["ttl"])
				}
				if cc["scope"] != "global" {
					t.Errorf("last system cache_control scope: want global got %v", cc["scope"])
				}
			}

			// 4. metadata.user_id must be present and JSON-shaped, with the
			//    real account_uuid we passed in.
			var md map[string]json.RawMessage
			if err := json.Unmarshal(parsed["metadata"], &md); err != nil {
				t.Fatalf("metadata not object: %v", err)
			}
			var uidStr string
			if err := json.Unmarshal(md["user_id"], &uidStr); err != nil {
				t.Fatalf("metadata.user_id not string: %v", err)
			}
			var uidObj map[string]string
			if err := json.Unmarshal([]byte(uidStr), &uidObj); err != nil {
				t.Fatalf("metadata.user_id not JSON-encoded: %v (%q)", err, uidStr)
			}
			if len(uidObj["device_id"]) != 64 {
				t.Errorf("device_id wrong length: %d", len(uidObj["device_id"]))
			}
			if uidObj["session_id"] == "" {
				t.Errorf("session_id empty")
			}
			if uidObj["account_uuid"] != "00000000-0000-0000-0000-000000000000" {
				t.Errorf("account_uuid not propagated: %q", uidObj["account_uuid"])
			}

			// 5. Last message has cache_control on its last content block.
			var msgs []map[string]json.RawMessage
			if err := json.Unmarshal(parsed["messages"], &msgs); err != nil {
				t.Fatalf("messages not array: %v", err)
			}
			if len(msgs) == 0 {
				t.Fatalf("messages empty after rewrite")
			}
			last := msgs[len(msgs)-1]
			var lastBlocks []map[string]any
			if err := json.Unmarshal(last["content"], &lastBlocks); err != nil {
				t.Fatalf("last message content not array: %v", err)
			}
			if len(lastBlocks) == 0 {
				t.Fatalf("last message has no content blocks")
			}
			if _, ok := lastBlocks[len(lastBlocks)-1]["cache_control"]; !ok {
				t.Errorf("last content block missing cache_control")
			}
		})
	}
}

// TestPerAccountStability asserts the core security-experiment invariant:
// device_id depends ONLY on the account anchor, not on the downstream
// client token. Two different downstream users hitting the same account
// must present identical device_ids (same device, multiple windows) and
// distinct session_ids (separate concurrent sessions).
func TestPerAccountStability(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}]}`)
	user1 := SimIdentity{AccountKey: "alice@example.com", AccountUUID: "u-alice", ClientToken: "client-A"}
	user2 := SimIdentity{AccountKey: "alice@example.com", AccountUUID: "u-alice", ClientToken: "client-B"}

	d1 := DeviceIDFor(user1.AccountKey)
	d2 := DeviceIDFor(user2.AccountKey)
	if d1 != d2 {
		t.Errorf("same account must yield same device_id: %s vs %s", d1, d2)
	}

	s1 := SessionIDFor(user1, body)
	s2 := SessionIDFor(user2, body)
	if s1 == s2 {
		t.Errorf("different downstream clients must yield different session_ids")
	}

	// Different account → different device.
	user3 := SimIdentity{AccountKey: "bob@example.com", AccountUUID: "u-bob", ClientToken: "client-A"}
	if DeviceIDFor(user3.AccountKey) == d1 {
		t.Errorf("different accounts must yield different device_ids")
	}
}

// TestSessionStableAcrossTurns asserts that the session_id stays stable as
// a multi-turn conversation grows (matching a real `claude` invocation),
// because it's keyed on the first user message rather than the full body.
func TestSessionStableAcrossTurns(t *testing.T) {
	id := SimIdentity{AccountKey: "alice@example.com", AccountUUID: "u-alice", ClientToken: "client-A"}
	turn1 := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"first question"}]}`)
	turn2 := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"first question"},{"role":"assistant","content":"answer"},{"role":"user","content":"follow up"}]}`)
	turn3New := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"completely different first question"}]}`)

	s1 := SessionIDFor(id, turn1)
	s2 := SessionIDFor(id, turn2)
	if s1 != s2 {
		t.Errorf("session_id must stay stable across turns of one conversation: %s vs %s", s1, s2)
	}
	s3 := SessionIDFor(id, turn3New)
	if s1 == s3 {
		t.Errorf("starting a new conversation must rotate session_id")
	}
}

// TestHaikuSkip confirms Haiku models bypass body rewriting entirely
// (Anthropic doesn't third-party-check Haiku).
func TestHaikuSkip(t *testing.T) {
	in := `{"model":"claude-haiku-4-5","system":"sys","messages":[{"role":"user","content":"hi"}]}`
	out := applyClaudeCodeBodyMimicry([]byte(in), "claude-haiku-4-5-20251001", testID())
	if string(out) != in {
		t.Errorf("haiku body was modified — should be passthrough\nin=%s\nout=%s", in, out)
	}
}

// TestAlreadyClaudeCode confirms we don't double-rewrite a request whose
// system already starts with the Claude Code prompt.
func TestAlreadyClaudeCode(t *testing.T) {
	in := `{"model":"claude-sonnet-4-5","system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude. Stuff."}],"messages":[{"role":"user","content":"hi"}]}`
	out := applyClaudeCodeBodyMimicry([]byte(in), "claude-sonnet-4-5", testID())

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("invalid output JSON: %v", err)
	}
	var sys []map[string]any
	if err := json.Unmarshal(parsed["system"], &sys); err != nil {
		t.Fatalf("system not array: %v", err)
	}
	if len(sys) != 1 {
		t.Errorf("already-CC system should not be rewritten, got %d blocks", len(sys))
	}
}

// TestNoBetaGatedFieldInjection confirms we do NOT synthesize beta-gated
// body fields (thinking / output_config / context_management) when the
// downstream client didn't send them. Real CC carries these but each is
// gated by a beta header that only some clients negotiate; injecting
// blindly would 400 the request whenever the client's anthropic-beta
// list lacks the matching marker, which is both worse for the user AND
// a stronger fingerprint than omitting them.
func TestNoBetaGatedFieldInjection(t *testing.T) {
	in := `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`
	out := applyClaudeCodeBodyMimicry([]byte(in), "claude-sonnet-4-5", testID())
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("invalid output JSON: %v", err)
	}
	for _, key := range []string{"thinking", "output_config", "context_management"} {
		if _, ok := parsed[key]; ok {
			t.Errorf("%s was synthesized but client didn't send it — this should be omitted to avoid breaking non-CC clients on beta-gated fields", key)
		}
	}
}

// TestClientBetaGatedFieldsPassThrough confirms client-supplied values for
// beta-gated fields are preserved verbatim — we don't strip them either.
func TestClientBetaGatedFieldsPassThrough(t *testing.T) {
	in := `{"model":"claude-sonnet-4-5","thinking":{"type":"enabled","budget_tokens":16000},"messages":[{"role":"user","content":"hi"}]}`
	out := applyClaudeCodeBodyMimicry([]byte(in), "claude-sonnet-4-5", testID())
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("invalid output JSON: %v", err)
	}
	if string(parsed["thinking"]) != `{"type":"enabled","budget_tokens":16000}` {
		t.Errorf("thinking was modified: %s", parsed["thinking"])
	}
}

// TestCCHSigning asserts the cch field is replaced with a 5-hex digest
// derived from body content (different bodies → different cch).
func TestCCHSigning(t *testing.T) {
	a := applyClaudeCodeBodyMimicry(
		[]byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"alpha"}]}`),
		"claude-sonnet-4-5", testID(),
	)
	b := applyClaudeCodeBodyMimicry(
		[]byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"beta"}]}`),
		"claude-sonnet-4-5", testID(),
	)
	if string(a) == string(b) {
		t.Fatalf("two distinct bodies produced identical output — cch not signing body content")
	}
}
