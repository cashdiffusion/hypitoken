package server

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// requireClaudeCodeClient is a gin middleware that rejects requests not
// originating from the official Claude Code product family. The gateway
// only fronts Claude models for users running official Claude Code clients
// (CLI, VSCode/JetBrains extension, Web) — third-party tools (raw Anthropic
// SDK, LiteLLM, custom chat clients, …) are blocked here at ingress so they
// can't bypass the per-credential mimicry layer downstream and trigger
// third-party-detection on real Claude accounts.
//
// The official Claude Code product ships *two* user-agent shapes on
// /v1/messages, depending on which surface emits the call:
//
//  1. CLI path — Anthropic-SDK-driven:
//     User-Agent: claude-cli/<v> (external, cli)
//     and the full SDK fingerprint set (x-app: cli, x-stainless-*,
//     x-claude-code-session-id UUID).
//
//  2. Non-CLI path — direct fetch/axios from IDE/Web surfaces:
//     User-Agent: claude-code/<v>
//     SDK fingerprint set is NOT carried (these surfaces don't run the
//     Anthropic SDK; they use fetch/axios directly). They still set
//     Anthropic-Beta with a claude-code-* / oauth-202X-* token.
//
// We accept either shape; for the CLI shape we enforce the full SDK
// fingerprint; for the non-CLI shape only UA + Anthropic-Beta are
// mandated. Live captures of CC 2.1.146 confirm both shapes appear from
// the same official binary (e.g. /v1/messages uses claude-cli, while
// /api/event_logging/v2/batch from the same process uses claude-code).
func requireClaudeCodeClient() gin.HandlerFunc {
	return func(c *gin.Context) {
		if reason, ok := matchClaudeCodeClient(c.Request.Header); !ok {
			h := c.Request.Header
			log.Warnf("client-fp: rejecting non-Claude-Code request to %s — %s — UA=%q x-app=%q beta=%q sid=%q stainless-ver=%q",
				c.Request.URL.Path, reason,
				h.Get("User-Agent"), h.Get("X-App"),
				h.Get("Anthropic-Beta"), h.Get("X-Claude-Code-Session-Id"),
				h.Get("X-Stainless-Package-Version"))
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "forbidden",
					"message": "client not allowed: this gateway only accepts the official Claude Code product family (https://claude.com/claude-code) — CLI, IDE extension, or Web. Third-party SDKs, LiteLLM, and custom clients are blocked.",
					"reason":  reason,
				},
			})
			return
		}
		c.Next()
	}
}

// claudeCliUARe is the CLI's SDK-driven UA. The "(external, cli)" suffix
// is the distinguishing marker that Anthropic-SDK reproduces; LiteLLM and
// raw SDK do not.
var claudeCliUARe = regexp.MustCompile(`^claude-cli/\d+\.\d+\.\d+\s+\(external,\s*cli\)$`)

// claudeCodeUARe is the non-CLI UA shape used by IDE extensions and the
// Web surface (and, observed in captures, by some of the CLI's own
// non-SDK sidecar requests like /api/event_logging). An optional
// parenthesized suffix is allowed for future drift (e.g. "(vscode)",
// "(web)").
var claudeCodeUARe = regexp.MustCompile(`^claude-code/\d+\.\d+\.\d+(\s+\([^)]*\))?$`)

// uuidLooseRe is a permissive UUID matcher (8-4-4-4-12 hex). CC's session
// id and request id both use this format. Don't tighten — uuid v4 vs v7
// drift across releases.
var uuidLooseRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// matchClaudeCodeClient returns ("", true) when the request looks like a
// real CC client and (reason, false) otherwise. The reason is exposed in
// the rejection JSON so legitimate CC users hitting an upgrade-driven
// regression have something actionable.
func matchClaudeCodeClient(h http.Header) (string, bool) {
	ua := strings.TrimSpace(h.Get("User-Agent"))
	isCli := claudeCliUARe.MatchString(ua)
	isCode := claudeCodeUARe.MatchString(ua)
	if !isCli && !isCode {
		return "user-agent must be `claude-cli/<v> (external, cli)` or `claude-code/<v>`", false
	}
	// For the CLI/SDK path, enforce the full Anthropic-SDK fingerprint
	// set. For the non-CLI shape (IDE/Web), only UA + Anthropic-Beta
	// are mandated: those surfaces don't run the Anthropic SDK so they
	// don't carry x-stainless-* or x-claude-code-session-id.
	if isCli {
		if !strings.EqualFold(strings.TrimSpace(h.Get("X-App")), "cli") {
			return "x-app must be `cli` (cli UA path)", false
		}
		if !strings.EqualFold(strings.TrimSpace(h.Get("X-Stainless-Lang")), "js") {
			return "x-stainless-lang must be `js` (cli UA path)", false
		}
		if !strings.EqualFold(strings.TrimSpace(h.Get("X-Stainless-Runtime")), "node") {
			return "x-stainless-runtime must be `node` (cli UA path)", false
		}
		if strings.TrimSpace(h.Get("X-Stainless-Package-Version")) == "" {
			return "x-stainless-package-version is required (cli UA path)", false
		}
		if !uuidLooseRe.MatchString(strings.TrimSpace(h.Get("X-Claude-Code-Session-Id"))) {
			return "x-claude-code-session-id must be a UUID (cli UA path)", false
		}
	}
	beta := strings.ToLower(h.Get("Anthropic-Beta"))
	if beta == "" {
		return "anthropic-beta header is required", false
	}
	// Real CC carries one of these tokens on every /v1/messages call —
	// `claude-code-20250219` (api-key mode), `oauth-2025-04-20` (oauth
	// mode), or a future `oauth-202X-*` rotation. Together they cover
	// both authentication paths and survive yearly oauth-beta rotation.
	if !strings.Contains(beta, "claude-code-") && !strings.Contains(beta, "oauth-2025-") && !strings.Contains(beta, "oauth-2026-") {
		return "anthropic-beta must include claude-code-* or oauth-202X-* token", false
	}
	return "", true
}
