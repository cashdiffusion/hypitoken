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
func (c *Checker) probe(ctx context.Context, a *auth.Auth, provider, model string) (status, errMsg string) {
	cli := auth.ClientFor(a.ProxyURL, false)
	cliCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
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

// probeOpenAI sends a minimal /v1/responses request — same shape real Codex
// CLI uses — with the cheapest possible billing footprint for a gpt-5 model:
//   - max_output_tokens: 1   (the gpt-5 family ignores max_tokens; this is
//     the correct param name on the responses endpoint).
//   - reasoning.effort: "minimal"  (default is medium, which silently spends
//     ~200+ hidden reasoning tokens per request even for trivial prompts).
//   - stream: false           (no need for SSE on a one-token probe).
//   - store: false            (don't persist on OpenAI-side).
// Verified against the request shapes documented in
// internal/server/codex_oauth_proxy.go — same endpoint real Codex CLI hits.
func (c *Checker) probeOpenAI(ctx context.Context, cli *http.Client, a *auth.Auth, model string) (string, string) {
	body := map[string]any{
		"model":              model,
		"input":              "ping",
		"max_output_tokens":  16, // 1 is rejected by some gateways; 16 is still ~$0.0001
		"reasoning":          map[string]any{"effort": "minimal"},
		"stream":             false,
		"store":              false,
	}
	buf, _ := json.Marshal(body)

	snap := a.Snapshot()
	base := strings.TrimRight(snap.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/responses", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.AccessToken)

	resp, err := cli.Do(req)
	if err != nil {
		// Some 3rd-party apikey gateways (tcdmx etc.) only implement
		// /v1/chat/completions, not /v1/responses. Fall back so we don't
		// flag those as unhealthy when they really are working.
		return c.probeOpenAIChatFallback(ctx, cli, a, model)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		return "ok", ""
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	// 404 / "not implemented" → fall back to chat/completions.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return c.probeOpenAIChatFallback(ctx, cli, a, model)
	}
	return "fail", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
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
