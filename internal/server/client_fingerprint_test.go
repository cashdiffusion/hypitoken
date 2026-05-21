package server

import (
	"net/http"
	"testing"
)

// realClaudeCodeHeaders is a snapshot of a real CC 2.1.146 OAuth client
// /v1/messages request (whistle dump 2026-05-21, row 018). The fingerprint
// gate must accept this verbatim.
func realClaudeCodeHeaders() http.Header {
	h := http.Header{}
	h.Set("User-Agent", "claude-cli/2.1.146 (external, cli)")
	h.Set("X-App", "cli")
	h.Set("X-Stainless-Lang", "js")
	h.Set("X-Stainless-Runtime", "node")
	h.Set("X-Stainless-Package-Version", "0.94.0")
	h.Set("X-Claude-Code-Session-Id", "f332de63-dc4b-4667-b73e-ef0447c4e223")
	h.Set("Anthropic-Beta", "oauth-2025-04-20,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05")
	return h
}

// realClaudeCodeIDEHeaders is what an IDE/Web Claude Code surface sends:
// `claude-code/<v>` UA with no Anthropic-SDK stainless fingerprint, since
// those surfaces use fetch/axios directly. Only UA and Anthropic-Beta are
// guaranteed. Modelled after the `claude-code/2.1.146` UA observed in the
// same 2026-05-21 capture (e.g. /api/event_logging from the official CLI).
func realClaudeCodeIDEHeaders() http.Header {
	h := http.Header{}
	h.Set("User-Agent", "claude-code/2.1.146")
	h.Set("Anthropic-Beta", "oauth-2025-04-20")
	return h
}

func TestFingerprint_AcceptsRealCC(t *testing.T) {
	if _, ok := matchClaudeCodeClient(realClaudeCodeHeaders()); !ok {
		t.Fatal("real CC headers should pass")
	}
}

// API-key-mode CC carries `claude-code-20250219` instead of `oauth-2025-04-20`
// in anthropic-beta. Both must work.
func TestFingerprint_AcceptsAPIKeyMode(t *testing.T) {
	h := realClaudeCodeHeaders()
	h.Set("Anthropic-Beta", "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05")
	if _, ok := matchClaudeCodeClient(h); !ok {
		t.Fatal("api-key-mode CC headers should pass")
	}
}

// IDE/Web surfaces of the official Claude Code product family ship `claude-code/<v>`
// without the SDK stainless headers — they must still pass.
func TestFingerprint_AcceptsIDEUA(t *testing.T) {
	if _, ok := matchClaudeCodeClient(realClaudeCodeIDEHeaders()); !ok {
		t.Fatal("claude-code/<v> IDE UA + Anthropic-Beta should pass")
	}
}

// Non-CLI UA without Anthropic-Beta must still be rejected — the relaxation
// for IDE/Web surfaces does not turn into a free-pass on UA spoof.
func TestFingerprint_RejectsIDEUAWithoutBeta(t *testing.T) {
	h := http.Header{}
	h.Set("User-Agent", "claude-code/2.1.146")
	if _, ok := matchClaudeCodeClient(h); ok {
		t.Fatal("claude-code/<v> without Anthropic-Beta must reject")
	}
}

func TestFingerprint_RejectsLiteLLM(t *testing.T) {
	// LiteLLM forwarder typical headers — reuses the Anthropic SDK
	// underneath but identifies itself in the User-Agent.
	h := http.Header{}
	h.Set("User-Agent", "litellm/1.50.0 anthropic/0.40.0")
	h.Set("X-Stainless-Lang", "python")
	h.Set("X-Stainless-Runtime", "python")
	h.Set("Anthropic-Beta", "computer-use-2025-01-24")
	if reason, ok := matchClaudeCodeClient(h); ok {
		t.Fatal("LiteLLM must be rejected")
	} else {
		t.Logf("rejected as expected: %s", reason)
	}
}

func TestFingerprint_RejectsAnthropicSDK(t *testing.T) {
	// Plain anthropic-sdk-python — no x-app, no claude-code-session-id.
	h := http.Header{}
	h.Set("User-Agent", "Anthropic/Python 0.40.0")
	h.Set("X-Stainless-Lang", "python")
	h.Set("X-Stainless-Runtime", "python")
	h.Set("X-Stainless-Package-Version", "0.40.0")
	if _, ok := matchClaudeCodeClient(h); ok {
		t.Fatal("Anthropic SDK must be rejected")
	}
}

func TestFingerprint_RejectsCurl(t *testing.T) {
	h := http.Header{}
	h.Set("User-Agent", "curl/8.4.0")
	if _, ok := matchClaudeCodeClient(h); ok {
		t.Fatal("curl must be rejected")
	}
}

// Defense in depth — even an attacker copy-pasting the User-Agent must also
// supply the other headers. Single-field UA spoof is the most common
// mistake; check it doesn't let them through.
func TestFingerprint_RejectsUASpoofAlone(t *testing.T) {
	h := http.Header{}
	h.Set("User-Agent", "claude-cli/2.1.126 (external, cli)")
	if reason, ok := matchClaudeCodeClient(h); ok {
		t.Fatal("UA-spoof-only must be rejected")
	} else {
		t.Logf("rejected as expected: %s", reason)
	}
}

// Each missing field individually rejects.
func TestFingerprint_RejectsMissingField(t *testing.T) {
	stripFields := []string{
		"X-App",
		"X-Stainless-Lang",
		"X-Stainless-Runtime",
		"X-Stainless-Package-Version",
		"X-Claude-Code-Session-Id",
		"Anthropic-Beta",
	}
	for _, f := range stripFields {
		t.Run(f, func(t *testing.T) {
			h := realClaudeCodeHeaders()
			h.Del(f)
			if _, ok := matchClaudeCodeClient(h); ok {
				t.Fatalf("missing %s must reject", f)
			}
		})
	}
}

// Bad UA shapes — version stripped, suffix changed, etc.
func TestFingerprint_RejectsMalformedUA(t *testing.T) {
	cases := []string{
		"claude-cli/2.1.126",                       // no "(external, cli)"
		"claude-cli/2.1.126 (external)",            // missing cli marker
		"Claude-Cli/2.1.126 (external, cli)",       // wrong case prefix
		"claude-cli (external, cli)",               // no version
		"my-claude-cli/2.1.126 (external, cli)",    // prefix
		"claude-cli/abc.def.ghi (external, cli)",   // non-numeric version
	}
	for _, ua := range cases {
		t.Run(ua, func(t *testing.T) {
			h := realClaudeCodeHeaders()
			h.Set("User-Agent", ua)
			if _, ok := matchClaudeCodeClient(h); ok {
				t.Fatalf("UA %q must be rejected", ua)
			}
		})
	}
}
