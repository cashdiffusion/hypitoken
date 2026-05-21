package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// Header values pinned to Claude Code 2.1.146 / @anthropic-ai/sdk 0.94.0.
// Values verified against a live CC 2.1.146 OAuth session capture
// (whistle dump 2026-05-21, rows 018/028/037/044).
// CLICurrentVersion below MUST match the version baked into claudeCLIUserAgent;
// any drift will cause the cc_version=X.Y.Z.{fp} billing block to disagree
// with the User-Agent and trigger Anthropic's third-party detection.
const (
	CLICurrentVersion       = "2.1.146"
	claudeCLIUserAgent      = "claude-cli/2.1.146 (external, cli)"
	claudeStainlessLang     = "js"
	claudeStainlessRuntime  = "node"
	claudeStainlessRuntimeV = "v24.3.0"
	claudeStainlessPackageV = "0.94.0"
	claudeStainlessOS       = "Linux"
	claudeStainlessArch     = "x64"
	claudeStainlessTimeout  = "600"
	claudeStainlessRetryCnt = "0"
	claudeAnthropicVersion  = "2023-06-01"
	// Beta list captured from real CC 2.1.146 — exact value, exact order.
	// Any beta we drop that real CLI sends will downgrade us to "extra usage"
	// billing; any extra beta we add that real CLI does not send is also a
	// fingerprint signal Anthropic edges look for.
	//
	// OAuth path (subscription accounts hitting api.anthropic.com):
	// captured live from CC 2.1.146 on 2026-05-21.
	// 2.1.141→2.1.146 diff: re-added context-1m-2025-08-07 (positioned
	// immediately after oauth-2025-04-20).
	claudeAnthropicBetaFull = "claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05,advisor-tool-2026-03-01,advanced-tool-use-2025-11-20,effort-2025-11-24,extended-cache-ttl-2025-04-11,cache-diagnosis-2026-04-07"

	// API-key path (real CC pointed at a 3rd-party gateway with x-api-key):
	// captured from crack/apikey/rows/*-POST-…v1_messages.json. Strict
	// gateways (fucheers, etc.) reject any unknown beta token — notably they
	// reject advanced-tool-use-* and cache-diagnosis-*, which real CC does
	// NOT send on the apikey path. Keep this list verbatim from capture.
	claudeAnthropicBetaApikey = "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05,advisor-tool-2026-03-01,context-1m-2025-08-07,effort-2025-11-24"
)

// Default cache_control TTL for cache breakpoints we inject. Real CC 2.1.126
// uses "1h" with scope=global on the heavy system blocks — match it so prefix
// caching works the same way and the request shape is byte-identical.
const claudeDefaultCacheTTL = "1h"
const claudeDefaultCacheScope = "global"

// Claude Code system prompt — first system block on every real CLI request.
const claudeCodeSystemPrompt = "You are Claude Code, Anthropic's official CLI for Claude."

// claudeCodePromptPrefixes detects requests whose system field already looks
// like a Claude Code request — we leave those alone (don't double-inject).
// Mirrors sub2api/internal/service/gateway_service.go.
var claudeCodePromptPrefixes = []string{
	"You are Claude Code, Anthropic's official CLI for Claude",
	"You are a Claude agent, built on Anthropic's Claude Agent SDK",
	"You are a file search specialist for Claude Code",
	"You are a helpful AI assistant tasked with summarizing conversations",
}

func newRequestUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is essentially impossible; fall back to a
		// deterministic string so the request still ships.
		return "00000000-0000-4000-8000-000000000000"
	}
	return uuidFromBytes(b[:])
}

func uuidFromBytes(b []byte) string {
	out := make([]byte, 16)
	copy(out, b)
	out[6] = (out[6] & 0x0f) | 0x40 // version 4
	out[8] = (out[8] & 0x3f) | 0x80 // variant RFC 4122
	hexs := hex.EncodeToString(out)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexs[0:8], hexs[8:12], hexs[12:16], hexs[16:20], hexs[20:32])
}

// ensureHeader sets name=value only if the header isn't already set. Mirrors
// upstream CLIProxyAPI's misc.EnsureHeader so client-supplied values (already
// copied in by copyForwardableHeaders) win over our defaults.
func ensureHeader(h http.Header, name, value string) {
	if strings.TrimSpace(h.Get(name)) == "" {
		h.Set(name, value)
	}
}
