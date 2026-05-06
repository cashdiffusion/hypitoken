// Package health probes upstream credentials (both OAuth and API-key) using
// the standard Claude Code 2.1.126 request format captured in crack/oauth/.
// Only claude-haiku is probed per credential — it's the cheapest model and a
// successful probe implies the credential is usable for all Claude models.
package health

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// anthropicProbeModel is the only Anthropic model we actually hit.
// One successful probe means the credential works for all Claude models.
const anthropicProbeModel = "claude-haiku-4-5-20251001"

// codexProbeModel is the Codex model we probe. Empirically gpt-5.5 is the
// only model in the GPT-5 family that's reliably accessible across the
// upstream gateways we use, so we probe it instead of gpt-5.3-codex.
// Switching here also clears stale model_health rows on next cycle via
// PruneModelHealthOtherModels.
const codexProbeModel = "gpt-5.5"

type Checker struct {
	DB       *db.DB
	Pool     *auth.Pool
	Interval time.Duration
	mu       sync.Mutex
	running  bool
}

func New(store *db.DB, pool *auth.Pool, interval time.Duration) *Checker {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	return &Checker{DB: store, Pool: pool, Interval: interval}
}

// Run starts the periodic checker. Cancel ctx to stop.
func (c *Checker) Run(ctx context.Context) {
	t := time.NewTicker(c.Interval)
	defer t.Stop()
	c.RunOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.RunOnce(ctx)
		}
	}
}

// RunOnce probes every credential once. Safe to call concurrently.
func (c *Checker) RunOnce(ctx context.Context) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.running = false; c.mu.Unlock() }()

	for _, st := range c.Pool.Status() {
		a := c.Pool.FindByID(st.Auth.ID)
		if a == nil {
			continue
		}
		provider := auth.NormalizeProvider(a.Provider)
		var model string
		switch provider {
		case auth.ProviderAnthropic:
			model = anthropicProbeModel
		case auth.ProviderOpenAI:
			model = codexProbeModel
		default:
			continue
		}
		// Drop any stale rows left over from a previous probe-model choice.
		// Without this, switching from e.g. gpt-4o-mini to gpt-5.3-codex
		// leaves the old (auth_id, "gpt-4o-mini") row behind and the status
		// page double-counts the credential.
		if err := c.DB.PruneModelHealthOtherModels(ctx, a.ID, []string{model}); err != nil {
			log.Warnf("health: prune stale rows for %s: %v", a.ID, err)
		}
		c.checkOne(ctx, a, provider, model)
	}
}

// Refresh kicks off RunOnce in the background.
func (c *Checker) Refresh() { go c.RunOnce(context.Background()) }

func (c *Checker) checkOne(ctx context.Context, a *auth.Auth, provider, model string) {
	t := time.Now()
	st, errMsg := c.probe(ctx, a, provider, model)
	latency := int(time.Since(t).Milliseconds())
	rec := db.ModelHealth{
		AuthID:    a.ID,
		Provider:  provider,
		Model:     model,
		Status:    st,
		LatencyMs: latency,
		Error:     errMsg,
	}
	if err := c.DB.UpsertModelHealth(ctx, rec); err != nil {
		log.Warnf("health: upsert %s/%s: %v", a.ID, model, err)
	}
	if err := c.DB.AppendModelHealthHistory(ctx, rec); err != nil {
		log.Warnf("health: history %s/%s: %v", a.ID, model, err)
	}
	log.Infof("health probe %s/%s %s → %s (%dms) %s", a.ID, model, provider, st, latency, errMsg)
}

// probe sends a Claude Code–style request to the upstream and returns
// ("ok", "") on success or ("fail", reason) on any error.
//
// Codex / GPT-5 streams take longer than the typical 20s budget — empirically
// gpt-5.5 first-byte latency on third-party gateways is 10-30s and full
// stream completion is up to 90s. Apply per-provider timeouts so a slow
// reasoning model doesn't get flagged as down.
func (c *Checker) probe(ctx context.Context, a *auth.Auth, provider, model string) (status, errMsg string) {
	cli := auth.ClientFor(a.ProxyURL, false)
	timeout := 20 * time.Second
	if provider == auth.ProviderOpenAI {
		timeout = 120 * time.Second
	}
	cliCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch provider {
	case auth.ProviderAnthropic:
		return c.probeAnthropic(cliCtx, cli, a, model)
	case auth.ProviderOpenAI:
		return c.probeOpenAI(cliCtx, cli, a, model)
	}
	return "fail", "unknown provider"
}

// probeAnthropic sends a standard Claude Code quota-probe request.
// Format matches crack/oauth/rows/06-POST-api.anthropic.com_v1_messages.json
// (the sidecar's quota probe) — same shape real CC sends on startup.
func (c *Checker) probeAnthropic(ctx context.Context, cli *http.Client, a *auth.Auth, model string) (string, string) {
	sessionID := newUUID()
	deviceID := strings.Repeat("0", 64)
	accountUUID := "00000000-0000-0000-0000-000000000001"

	// metadata.user_id is a JSON-encoded string, exactly as CC sends it.
	userIDObj := map[string]string{
		"device_id":    deviceID,
		"account_uuid": accountUUID,
		"session_id":   sessionID,
	}
	userIDJSON, _ := json.Marshal(userIDObj)

	body := map[string]any{
		"model":      model,
		"max_tokens": 1,
		"messages": []any{map[string]any{
			"role":    "user",
			"content": "quota",
		}},
		"metadata": map[string]any{
			"user_id": string(userIDJSON),
		},
	}
	buf, _ := json.Marshal(body)

	snap := a.Snapshot()
	base := strings.TrimRight(snap.BaseURL, "/")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, br")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
	req.Header.Set("X-App", "cli")
	req.Header.Set("X-Claude-Code-Session-Id", sessionID)
	req.Header.Set("X-Client-Request-Id", newUUID())
	req.Header.Set("X-Stainless-Arch", "x64")
	req.Header.Set("X-Stainless-Lang", "js")
	req.Header.Set("X-Stainless-OS", "Linux")
	req.Header.Set("X-Stainless-Package-Version", "0.81.0")
	req.Header.Set("X-Stainless-Retry-Count", "0")
	req.Header.Set("X-Stainless-Runtime", "node")
	req.Header.Set("X-Stainless-Runtime-Version", "v24.3.0")
	req.Header.Set("X-Stainless-Timeout", "600")
	req.Header.Set("User-Agent", "claude-cli/2.1.126 (external, cli)")

	isOAuth := a.Kind == auth.KindOAuth
	if isOAuth {
		// OAuth: Bearer token + oauth beta header
		req.Header.Set("Authorization", "Bearer "+a.AccessToken)
		req.Header.Set("anthropic-beta", "oauth-2025-04-20,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05")
	} else {
		// API key: x-api-key + full CC beta list.
		// Verbatim from crack/apikey/rows/14-POST-…v1_messages.json — the
		// strict third-party gateways (fucheers etc.) reject any unknown
		// token, so we use exactly what real CC 2.1.126 sends and nothing
		// more (no advanced-tool-use, no cache-diagnosis on the apikey path).
		req.Header.Set("X-Api-Key", a.AccessToken)
		req.Header.Set("anthropic-beta", "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,context-management-2025-06-27,prompt-caching-scope-2026-01-05,advisor-tool-2026-03-01,context-1m-2025-08-07,effort-2025-11-24")
	}

	resp, err := cli.Do(req)
	if err != nil {
		return "fail", err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "fail", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	// Drain enough to confirm no stream error, then close.
	head := make([]byte, 2048)
	n, _ := io.ReadFull(io.LimitReader(resp.Body, int64(len(head))), head)
	s := string(head[:n])
	if strings.Contains(s, `"type":"error"`) || strings.Contains(s, "event: error") {
		if len(s) > 200 {
			s = s[:200] + "…"
		}
		return "fail", "stream error: " + strings.TrimSpace(s)
	}
	return "ok", ""
}

// probeOpenAI sends a streaming /responses request shaped after a real
// Codex CLI capture (whistle dump of POST tcdmx.com/responses, gpt-5.5,
// Accept: text/event-stream).
//
// Why streaming: gpt-5 reasoning models can take 30s+ to first byte on a
// non-streaming call; on third-party gateways like tcdmx the buffered
// path also tends to time out at the gateway nginx default. SSE returns
// the first event in 2-5s and we accept any 200 with content-type
// text/event-stream as a healthy probe — no need to drain the full reply.
//
// Path cascade:
//  1. {base}/v1/responses   — official OpenAI / well-behaved compatibles
//  2. {base}/responses      — Codex-CLI-style gateways (tcdmx, etc) that
//                             mimic the ChatGPT internal path without /v1/
//  3. {base}/v1/chat/completions — old-style chat shim, last resort
//
// Each non-200 response triggers the next step. We only mark "fail" if
// every variant errors or all return 4xx/5xx.
func (c *Checker) probeOpenAI(ctx context.Context, cli *http.Client, a *auth.Auth, model string) (string, string) {
	snap := a.Snapshot()
	base := strings.TrimRight(snap.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com"
	}

	// /v1/responses → /responses (cascade for the responses-shaped body).
	for _, p := range []string{"/v1/responses", "/responses"} {
		st, msg, retry := c.probeOpenAIResponses(ctx, cli, a, model, base+p)
		if !retry {
			return st, msg
		}
	}
	// All responses-shaped paths returned 404/405/timeout — try the legacy
	// /v1/chat/completions shim before giving up.
	return c.probeOpenAIChatFallback(ctx, cli, a, model)
}

// probeOpenAIResponses POSTs the Codex /responses-shaped body to the
// given URL. Returns (status, error, retry):
//   - retry=true  → the URL didn't work (404/405 or transport error); the
//     caller should try the next path in the cascade.
//   - retry=false → the URL responded definitively (200 = ok, 4xx/5xx
//     other than 404/405 = the credential is bad and trying other paths
//     won't help).
func (c *Checker) probeOpenAIResponses(ctx context.Context, cli *http.Client, a *auth.Auth, model, url string) (status, errMsg string, retry bool) {
	body := map[string]any{
		"model":             model,
		"input":             "ping",
		"max_output_tokens": 16,
		"reasoning":         map[string]any{"effort": "minimal"},
		// Streaming mirrors what real Codex CLI sends. Gateways like tcdmx
		// require it — the buffered path 502s on long-running gpt-5 reasoning.
		"stream": true,
		"store":  false,
	}
	buf, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+a.AccessToken)

	resp, err := cli.Do(req)
	if err != nil {
		// Transport error (DNS, TLS, conn refused). Treat 404-equivalent —
		// caller can try the next path. Bubble the message up if the next
		// path also fails.
		return "fail", err.Error(), true
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		// Wrong path on this gateway — try the next.
		return "fail", fmt.Sprintf("HTTP %d", resp.StatusCode), true
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "fail", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b))), false
	}
	// 200 OK. For SSE we just want to confirm at least one event arrives so
	// we know the gateway is actually serving the model — don't drain the
	// whole stream (that costs full output_tokens). Read up to 4 KB or
	// until we see "event: " / "data: " — whichever first.
	head := make([]byte, 4096)
	n, _ := io.ReadFull(io.LimitReader(resp.Body, int64(len(head))), head)
	s := string(head[:n])
	if strings.Contains(s, `"type":"error"`) || strings.Contains(s, "event: error") {
		if len(s) > 200 {
			s = s[:200] + "…"
		}
		return "fail", "stream error: " + strings.TrimSpace(s), false
	}
	return "ok", "", false
}

// probeOpenAIChatFallback is used when /v1/responses isn't available on the
// upstream — most cheap apikey gateways only ship /v1/chat/completions.
// max_completion_tokens (not max_tokens) is the correct cap for gpt-5 family.
func (c *Checker) probeOpenAIChatFallback(ctx context.Context, cli *http.Client, a *auth.Auth, model string) (string, string) {
	body := map[string]any{
		"model":                 model,
		"messages":              []any{map[string]any{"role": "user", "content": "ping"}},
		"max_completion_tokens": 16,
		"reasoning_effort":      "minimal",
		"stream":                false,
	}
	buf, _ := json.Marshal(body)

	snap := a.Snapshot()
	base := strings.TrimRight(snap.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.AccessToken)

	resp, err := cli.Do(req)
	if err != nil {
		return "fail", err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		return "ok", ""
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return "fail", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}
