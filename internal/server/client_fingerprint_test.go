package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newGateCtx builds a gin context carrying the given request headers, plus the
// recorder the middleware writes its (possible) 403 to.
func newGateCtx(h http.Header) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header = h
	c.Request = req
	return c, w
}

// The ingress gate switched from a strict allowlist (claude-cli / claude-code
// only, full SDK fingerprint) to a shared blocklist (cc-core/clientguard):
// known SDK/scripting User-Agents are rejected, everything else — including
// interactive desktop/IDE clients we have no fingerprint for — passes. These
// tests pin the new contract.

func headersWithUA(ua string) http.Header {
	h := http.Header{}
	if ua != "" {
		h.Set("User-Agent", ua)
	}
	return h
}

// The official Claude Code family and other interactive clients must pass —
// note the relaxation: a bare claude-cli UA (no SDK fingerprint headers) and
// unknown desktop/IDE clients are now accepted by design.
func TestClientGate_AcceptsInteractiveClients(t *testing.T) {
	accept := []string{
		"claude-cli/2.1.146 (external, cli)",
		"claude-cli/2.1.158 (external, cli)",
		"claude-code/2.1.146",
		"claude-code/2.1.158 (vscode)",
		"Cursor/0.42.0",
		"Claude/1.2.3 Chrome/120 Electron/28 Safari/537.36", // Claude Desktop-ish
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
	}
	gate := requireClaudeCodeClient()
	for _, ua := range accept {
		c, w := newGateCtx(headersWithUA(ua))
		gate(c)
		if w.Code == http.StatusForbidden {
			t.Errorf("UA %q should be accepted, got 403", ua)
		}
	}
}

// Known SDK / scripting clients must be rejected.
func TestClientGate_RejectsAbuseClients(t *testing.T) {
	reject := []string{
		"litellm/1.50.0 anthropic/0.40.0",
		"Anthropic/Python 0.40.0",
		"Anthropic/JS 0.40.0",
		"OpenAI/Python 1.30.0",
		"python-requests/2.31.0",
		"python-httpx/0.27.0",
		"curl/8.4.0",
		"Go-http-client/2.0",
		"PostmanRuntime/7.36.0",
		"", // empty UA blocked by default
	}
	gate := requireClaudeCodeClient()
	for _, ua := range reject {
		c, w := newGateCtx(headersWithUA(ua))
		gate(c)
		if w.Code != http.StatusForbidden {
			t.Errorf("UA %q should be rejected with 403, got %d", ua, w.Code)
		}
	}
}
