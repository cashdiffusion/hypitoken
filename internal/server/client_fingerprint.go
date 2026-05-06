package server

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// requireClaudeCodeClient is a gin middleware that rejects requests not
// originating from the official Claude Code CLI. The gateway only fronts
// Claude models for users running `claude` — third-party tools (Anthropic
// SDK, LiteLLM, custom chat clients, …) are blocked here at ingress so they
// can't bypass the per-credential mimicry layer downstream and trigger
// third-party-detection on real Claude accounts.
//
// The fingerprint we require is the union of headers every captured CC
// 2.x release sends on /v1/messages — see crack/oauth/rows/06-*.json and
// crack/apikey/rows/14-*.json:
//
//   user-agent: claude-cli/<semver> (external, cli)
//   x-app: cli
//   x-stainless-lang: js
//   x-stainless-runtime: node
//   x-stainless-package-version: <semver>   (Anthropic SDK version)
//   x-claude-code-session-id: <uuid>
//   anthropic-beta: <list-containing-claude-code-* or oauth-*-2025>
//
// Any single one missing is enough to reject — they are all set
// unconditionally by the real CLI. Spoofing all six is possible but requires
// the attacker to actively impersonate Claude Code, which is exactly the
// behaviour we don't want to silently allow.
func requireClaudeCodeClient() gin.HandlerFunc {
	return func(c *gin.Context) {
		if reason, ok := matchClaudeCodeClient(c.Request.Header); !ok {
			ua := c.Request.Header.Get("User-Agent")
			log.Warnf("client-fp: rejecting non-Claude-Code request to %s — %s — UA=%q", c.Request.URL.Path, reason, ua)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "forbidden",
					"message": "client not allowed: this gateway only accepts the official Claude Code CLI (https://claude.com/claude-code). Third-party SDKs, LiteLLM, and custom clients are blocked.",
					"reason":  reason,
				},
			})
			return
		}
		c.Next()
	}
}

// claudeCodeUARe is the User-Agent shape real CC sends. Version is allowed
// to drift; the literal "(external, cli)" suffix is the part LiteLLM and
// the Anthropic SDK don't reproduce.
var claudeCodeUARe = regexp.MustCompile(`^claude-cli/\d+\.\d+\.\d+\s+\(external,\s*cli\)$`)

// uuidLooseRe is a permissive UUID matcher (8-4-4-4-12 hex). CC's session
// id and request id both use this format. Don't tighten — uuid v4 vs v7
// drift across releases.
var uuidLooseRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// matchClaudeCodeClient returns ("", true) when the request looks like a
// real CC client and (reason, false) otherwise. The reason is exposed in
// the rejection JSON so legitimate CC users hitting an upgrade-driven
// regression have something actionable.
func matchClaudeCodeClient(h http.Header) (string, bool) {
	if !claudeCodeUARe.MatchString(strings.TrimSpace(h.Get("User-Agent"))) {
		return "user-agent must be `claude-cli/<version> (external, cli)`", false
	}
	if !strings.EqualFold(strings.TrimSpace(h.Get("X-App")), "cli") {
		return "x-app must be `cli`", false
	}
	if !strings.EqualFold(strings.TrimSpace(h.Get("X-Stainless-Lang")), "js") {
		return "x-stainless-lang must be `js`", false
	}
	if !strings.EqualFold(strings.TrimSpace(h.Get("X-Stainless-Runtime")), "node") {
		return "x-stainless-runtime must be `node`", false
	}
	if strings.TrimSpace(h.Get("X-Stainless-Package-Version")) == "" {
		return "x-stainless-package-version is required", false
	}
	if !uuidLooseRe.MatchString(strings.TrimSpace(h.Get("X-Claude-Code-Session-Id"))) {
		return "x-claude-code-session-id must be a UUID", false
	}
	beta := strings.ToLower(h.Get("Anthropic-Beta"))
	if beta == "" {
		return "anthropic-beta header is required", false
	}
	// Real CC carries one of these tokens on every /v1/messages call —
	// `claude-code-20250219` (api-key mode) or `oauth-2025-04-20` (oauth
	// mode). Together they cover both authentication paths.
	if !strings.Contains(beta, "claude-code-") && !strings.Contains(beta, "oauth-2025-") {
		return "anthropic-beta must include claude-code-* or oauth-2025-* token", false
	}
	return "", true
}
