package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/wjsoj/cc-core/clientguard"
)

// requireClaudeCodeClient is a gin middleware that rejects requests from
// non-interactive SDK / scripting clients (raw Anthropic/OpenAI SDKs,
// LiteLLM, python-requests, curl, …) so they can't ride the per-credential
// mimicry layer downstream and trigger third-party-detection on real Claude
// accounts.
//
// Strategy is a **blocklist** (shared with CPA-Claude via cc-core/clientguard):
// requests whose User-Agent matches a known abuse fragment are rejected; every
// other client — the official Claude Code family (CLI, IDE/Web), Claude
// Desktop, Cursor, and any product client we don't have a fingerprint for —
// is allowed through. This deliberately replaced the earlier strict allowlist
// (claude-cli / claude-code only) so legitimate interactive desktop/IDE
// clients keep working without per-client rules. Because the downstream path
// reshapes the body to canonical CC form regardless, this filter is an
// access-policy layer that stops low-effort abuse, not a hard security
// boundary (a determined caller can spoof any User-Agent).
func requireClaudeCodeClient() gin.HandlerFunc {
	guard := clientguard.NewDefault()
	return func(c *gin.Context) {
		if d := guard.Inspect(c.Request.Header); d.Blocked {
			log.Warnf("client-fp: rejecting request to %s — %s — UA=%q",
				c.Request.URL.Path, d.Reason, c.Request.Header.Get("User-Agent"))
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "forbidden",
					"message": "client not allowed: this gateway only accepts interactive Claude clients (Claude Code, Claude Desktop, Cursor). Raw SDKs, LiteLLM, and scripting clients are blocked.",
					"reason":  d.Reason,
				},
			})
			return
		}
		c.Next()
	}
}
