package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/usage"
)

// The ChatGPT Codex backend expects the OpenAI /v1/responses schema with a
// handful of upstream-private fields stripped. The upstream request headers
// below mimic the Codex CLI fingerprint, pinned to codex-tui/0.135.0 and
// verified against a live ChatGPT Pro capture (cc-core/crack/codex/SPEC.md).
// We forward over the legacy HTTP POST /codex/responses path (OpenAI-Beta:
// responses=experimental); real 0.135.0 streams over a WebSocket, but the HTTP
// path still works and is what an HTTP-API proxy needs. We mimic the 0.135.0
// identity (Originator codex-tui / UA / Version) over it. (These Codex
// constants stay inline because this fork's Codex OAuth body/transport path
// diverged from cc-core's mimicry.ApplyCodexCLIHeaders; the Claude path now
// imports cc-core/mimicry directly — see applyAnthropicHeaders in proxy.go.)
const (
	codexCLIVersion        = "0.135.0"
	codexBackendUserAgent  = "codex-tui/0.135.0 (Arch Linux Rolling Release; x86_64) Konsole/260401 (codex-tui; 0.135.0)"
	codexBackendOriginator = "codex-tui"
)

// codexOAuthPath maps a client-facing path under /v1 to the corresponding
// suffix on the ChatGPT Codex backend (mounted under /codex). The backend
// hosts:
//   - /responses           — streaming inference (non-streaming clients are
//     satisfied via aggregateCodexResponseStream).
//   - /responses/compact   — Codex CLI's conversation-compaction endpoint;
//     body shape is the same /v1/responses payload,
//     so the same sanitize/transport path applies.
func codexOAuthPath(clientPath string) string {
	switch clientPath {
	case "/v1/responses/compact":
		return "/responses/compact"
	default:
		return "/responses"
	}
}

// sanitizeCodexRequestBody shapes the client's /v1/responses body into what
// the ChatGPT Codex backend expects. Behavior is modeled directly on
// CLIProxyAPI (translator/codex/openai/responses/codex_openai-responses_request.go
// + runtime/executor/codex_executor.go:Execute): the backend accepts a
// narrow subset of the OpenAI /v1/responses schema, so we force the
// required fields, delete the ones that get rejected, and normalize the
// payload shape. Upstream is always streamed — the `stream` bool on the
// client request does not change the body we send.
//
// clientPath selects the schema variant: /v1/responses/compact is a much
// stricter endpoint that only accepts {model, input, instructions,
// previous_response_id} — anything else (notably `include`,
// `context_management`, `tools`, `store`, `stream`) gets rejected with
// `Unknown parameter`. We mirror sub2api's normalizeOpenAICompactRequestBody
// for that path.
func sanitizeCodexRequestBody(body []byte, clientPath string) ([]byte, string, error) {
	if clientPath == "/v1/responses/compact" {
		return sanitizeCodexCompactRequestBody(body)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body, "", err
	}
	// Strip thinking suffix from model. CLIProxyAPI uses "model-name(value)"
	// convention (e.g. gpt-5.3-codex(high)); the backend wants just the
	// base model name. Plain model names are passed through untouched.
	baseModel := ""
	if m, ok := raw["model"].(string); ok {
		baseModel = stripThinkingSuffix(m)
		raw["model"] = baseModel
	}

	// Always stream upstream — the backend only emits completed responses
	// via SSE. Non-streaming clients get aggregation on our side.
	raw["stream"] = true

	// Required fields for the Codex backend.
	raw["store"] = false
	raw["parallel_tool_calls"] = true
	raw["include"] = []any{"reasoning.encrypted_content"}

	// Fields the backend rejects or that leak through from openai.com-
	// compatible SDKs but don't belong on the Codex backend. Note:
	// `previous_response_id` is intentionally NOT stripped — Codex CLI
	// chains multi-turn conversations on this field, and sub2api preserves
	// it for the same reason. Stripping it makes every turn a cold start
	// and may correlate with CF rate-limit bursts.
	for _, k := range []string{
		"prompt_cache_retention",
		"safety_identifier",
		"stream_options",
		"max_output_tokens",
		"max_completion_tokens",
		"temperature",
		"top_p",
		"truncation",
		"user",
		"context_management",
	} {
		delete(raw, k)
	}

	// service_tier: backend only honors "priority"; anything else 400s.
	if st, ok := raw["service_tier"].(string); ok && st != "priority" {
		delete(raw, "service_tier")
	}

	// Input may be a plain string on SDKs that use the convenience shape.
	// Promote to the canonical [{"type":"message","role":"user",...}] form.
	if s, ok := raw["input"].(string); ok {
		raw["input"] = []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": s,
			}},
		}}
	}
	// Convert role "system" → "developer" in input items (Codex rejects
	// "system" there).
	if items, ok := raw["input"].([]any); ok {
		for _, it := range items {
			if m, _ := it.(map[string]any); m != nil {
				if role, _ := m["role"].(string); role == "system" {
					m["role"] = "developer"
				}
			}
		}
	}

	// Normalize legacy/preview built-in tool type aliases.
	normalizeBuiltinToolsInPlace(raw)

	// Backfill empty instructions (backend requires the key to exist).
	if v, ok := raw["instructions"]; !ok || v == nil {
		raw["instructions"] = ""
	}

	// Ensure image_generation tool is present (matches vendor CLI; skipped
	// on *-spark models where the backend rejects it).
	raw["tools"] = ensureImageGenerationTool(raw["tools"], baseModel)

	out, err := json.Marshal(raw)
	return out, baseModel, err
}

// sanitizeCodexCompactRequestBody is the strict whitelist for the
// /codex/responses/compact endpoint. Mirrors sub2api's
// normalizeOpenAICompactRequestBody: the backend rejects everything except
// these four fields, so we drop the rest entirely (in particular
// `include`, `context_management`, `tools`, `store`, `stream`,
// `parallel_tool_calls` — all of which sanitizeCodexRequestBody force-
// injects for the regular /responses path and which would 400 here).
//
// The model field still has its CLIProxyAPI thinking-suffix stripped so
// `gpt-5.3-codex(high)` → `gpt-5.3-codex` for billing/upstream consistency.
func sanitizeCodexCompactRequestBody(body []byte) ([]byte, string, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body, "", err
	}
	baseModel := ""
	if m, ok := raw["model"].(string); ok {
		baseModel = stripThinkingSuffix(m)
	}
	out := map[string]any{}
	for _, k := range []string{"model", "input", "instructions", "previous_response_id"} {
		v, ok := raw[k]
		if !ok {
			continue
		}
		if k == "model" && baseModel != "" {
			out[k] = baseModel
			continue
		}
		out[k] = v
	}
	encoded, err := json.Marshal(out)
	return encoded, baseModel, err
}

// normalizeBuiltinToolsInPlace rewrites the legacy Codex built-in tool
// aliases to the stable names the backend accepts today. Mirrors
// CLIProxyAPI's normalizeCodexBuiltinTools.
func normalizeBuiltinToolsInPlace(raw map[string]any) {
	rewrite := func(m map[string]any) {
		if t, _ := m["type"].(string); t != "" {
			if n := normalizeBuiltinToolType(t); n != "" {
				m["type"] = n
			}
		}
	}
	if tools, ok := raw["tools"].([]any); ok {
		for _, t := range tools {
			if m, _ := t.(map[string]any); m != nil {
				rewrite(m)
			}
		}
	}
	if tc, ok := raw["tool_choice"].(map[string]any); ok {
		rewrite(tc)
		if inner, ok := tc["tools"].([]any); ok {
			for _, t := range inner {
				if m, _ := t.(map[string]any); m != nil {
					rewrite(m)
				}
			}
		}
	}
}

func normalizeBuiltinToolType(t string) string {
	switch t {
	case "web_search_preview", "web_search_preview_2025_03_11":
		return "web_search"
	}
	return ""
}

// stripThinkingSuffix mirrors thinking.ParseSuffix from CLIProxyAPI: a
// trailing "(value)" group (e.g. "gpt-5.3-codex(high)") is removed and the
// bare model name returned. Names without the suffix form are untouched.
func stripThinkingSuffix(model string) string {
	if !strings.HasSuffix(model, ")") {
		return model
	}
	i := strings.LastIndex(model, "(")
	if i <= 0 {
		return model
	}
	return model[:i]
}

// ensureImageGenerationTool guarantees the tools array has an entry of
// type=image_generation. The ChatGPT backend injects this server-side on
// the vendor CLI's requests; if we strip it (or the client omits it)
// responses with image-generation prompts fail. Skipped for "*-spark"
// models the backend rejects the tool on (matches CLIProxyAPI).
func ensureImageGenerationTool(current any, baseModel string) any {
	if strings.HasSuffix(baseModel, "spark") {
		if current == nil {
			return []any{}
		}
		return current
	}
	imageTool := map[string]any{"type": "image_generation", "output_format": "png"}
	arr, ok := current.([]any)
	if !ok || arr == nil {
		return []any{imageTool}
	}
	for _, t := range arr {
		if tm, _ := t.(map[string]any); tm != nil {
			if typ, _ := tm["type"].(string); typ == "image_generation" {
				return arr
			}
		}
	}
	return append(arr, imageTool)
}

// doForwardCodexOAuth forwards the client's /v1/responses request to the
// ChatGPT backend. Behavior matches the vendor Codex CLI: Bearer auth from
// the OAuth access_token, account_id from the cached ID-token claims, a
// fresh per-request session UUID, and the `codex-tui` User-Agent /
// Originator that the backend fingerprints on.
func (s *Server) doForwardCodexOAuth(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, clientName string, start time.Time, attempts int) (retry, done bool) {
	if path != "/v1/responses" && path != "/v1/responses/compact" {
		// The ChatGPT backend only hosts /codex/responses{,/compact}; OAuth
		// creds can't serve /v1/chat/completions. Ask the retry loop to try a
		// different credential (API-key creds handle chat/completions fine).
		// Don't MarkFailure — this credential isn't broken, just the wrong
		// kind. forward() has already fast-failed if no API-key alternatives
		// exist.
		log.Debugf("codex oauth: %s skipping %s (OAuth path supports /v1/responses{,/compact} only)", a.ID, path)
		return true, false
	}

	snap := a.Snapshot()
	baseURL := strings.TrimRight(s.cfg.ChatGPTBackendBaseURL, "/") + "/codex"
	// Per-credential base URL override is allowed for vendor-relay setups.
	if ab := strings.TrimRight(snap.BaseURL, "/"); ab != "" {
		baseURL = ab
	}
	upURL := baseURL + codexOAuthPath(path)

	upstreamBody, _, err := sanitizeCodexRequestBody(body, path)
	if err != nil {
		log.Warnf("codex oauth: body sanitize failed via %s: %v", a.ID, err)
		upstreamBody = body
	}

	ctx := c.Request.Context()
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upURL, bytes.NewReader(upstreamBody))
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": err.Error()})
		return false, true
	}
	copyForwardableHeaders(c.Request.Header, upReq.Header)

	accessToken, _ := a.Credentials()
	upReq.Header.Set("Authorization", "Bearer "+accessToken)
	upReq.Header.Set("Content-Type", "application/json")
	isCompactPath := path == "/v1/responses/compact"
	// /codex/responses streams SSE; /codex/responses/compact returns JSON.
	if isCompactPath {
		upReq.Header.Set("Accept", "application/json")
	} else {
		upReq.Header.Set("Accept", "text/event-stream")
	}
	// Version is part of the 0.135.0 identity — sent on every request now,
	// not just the compact endpoint.
	upReq.Header.Set("Version", codexCLIVersion)
	upReq.Header.Set("OpenAI-Beta", "responses=experimental")
	// Force plaintext upstream bodies. Otherwise CF may respond with br/gzip
	// which (a) breaks SSE streaming and (b) makes 4xx error bodies unreadable
	// in our logs. Identity keeps everything human-readable end-to-end.
	upReq.Header.Set("Accept-Encoding", "identity")
	upReq.Header.Set("Connection", "Keep-Alive")
	upReq.Header.Set("Session_id", mimicry.NewRequestUUID())
	upReq.Header.Set("Originator", codexBackendOriginator)
	// Always overwrite UA — forwarding client's UA (e.g. "curl/X.Y") makes
	// Cloudflare's edge rules 403 the request before it reaches the OpenAI
	// backend. We must look like the Codex CLI.
	upReq.Header.Set("User-Agent", codexBackendUserAgent)
	stripIngressHeaders(upReq.Header)
	if accountID, _ := a.CodexIdentity(); accountID != "" {
		upReq.Header.Set("Chatgpt-Account-Id", accountID)
	}

	// Shared pooled transport (per proxyURL). Reusing HTTP/2 connections is
	// critical here: chatgpt.com's CF edge rate-limits new TCP/TLS connections
	// from VPS/proxy IPs and RSTs the handshake when the per-IP new-connection
	// quota is hit — the classic alternating 200/503 symptom. A pooled h2 conn
	// carries many requests so we stay under the limit. ClientFor's transport
	// has HTTP/2 PING health checks (utls.go) so stale reused conns are
	// detected and re-dialed transparently.
	client := auth.ClientFor(snap.ProxyURL, s.cfg.UseUTLS)
	resp, err := client.Do(upReq)
	// Network-error retry on the SAME credential. These errors
	// (`connection reset by peer`, `broken pipe`, `EOF`, h2 GOAWAY) are
	// almost always transient infra flaps (CF new-conn rate limit, proxy
	// hiccup, stale h2 conn racing a server close) — not a problem with the
	// credential. We retry several times with increasing backoff so the
	// flap recovers without burning the cred via MarkFailure or surfacing as
	// "degraded" in the admin panel. The client's request context bounds the
	// loop — if the user gives up, we stop.
	const transientRetryBackoffsMs = 400
	transientBackoffs := []int{transientRetryBackoffsMs, 800, 1500, 2500}
	for attempt := 0; err != nil && attempt < len(transientBackoffs); attempt++ {
		if isClientDisconnect(ctx, err) || !isTransientNetErr(err) {
			break
		}
		log.Infof("codex oauth: transient net error via %s (attempt %d, will retry): %v", a.ID, attempt+1, err)
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(time.Duration(transientBackoffs[attempt]) * time.Millisecond):
		}
		if ctx.Err() != nil {
			break
		}
		retryReq, rerr := http.NewRequestWithContext(ctx, http.MethodPost, upURL, bytes.NewReader(upstreamBody))
		if rerr != nil {
			err = rerr
			break
		}
		retryReq.Header = upReq.Header.Clone()
		resp2, err2 := client.Do(retryReq)
		resp, err = resp2, err2
	}
	if err != nil {
		if isClientDisconnect(ctx, err) {
			a.MarkClientCancel(err.Error())
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.ProviderOpenAI,
				AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
				Stream: stream, Path: path, Status: 499, Attempts: attempts,
				DurationMs: time.Since(start).Milliseconds(),
				Error:      "client canceled",
			})
			return false, true
		}
		// Transient infra failure that survived the same-cred retry loop:
		// don't MarkFailure (would degrade the credential / show as unhealthy
		// in the admin panel), don't emit a request log row. Just ask the
		// outer loop to try another credential — and if that one is also the
		// only one, it'll come right back here for another round of retries.
		if isTransientNetErr(err) {
			log.Infof("codex oauth: transient net error survived same-cred retries via %s: %v (deferring to outer loop without MarkFailure)", a.ID, err)
			return true, false
		}
		a.MarkFailure(err.Error())
		log.Warnf("codex oauth: upstream error via %s: %v", a.ID, err)
		return true, false
	}

	// Capture rolling primary/secondary quota snapshot from upstream response
	// headers (the `x-codex-*` family). Done unconditionally since 4xx/429
	// responses also carry these — they're what tell us *why* we were blocked.
	a.CaptureCodexRateLimits(resp.Header)

	// Pre-read error bodies to inspect ChatGPT's usage-limit signals.
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusUnauthorized, http.StatusForbidden:
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resetAt := parseCodexResetAt(errBody)
		if resetAt.IsZero() {
			resetAt = parseRetryAfter(resp.Header)
		}
		s.pool.ReportUpstreamError(a, resp.StatusCode, resetAt)
		log.Warnf("codex oauth: credential %s received %d: %s", a.ID, resp.StatusCode, truncate(errBody, 240))
		return true, false
	}
	// Capacity errors come back with 200+JSON on some edge deployments or
	// as 4xx; the body message is what we actually key on.
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if isCodexCapacityError(errBody) {
			resetAt := parseCodexResetAt(errBody)
			s.pool.ReportUpstreamError(a, http.StatusTooManyRequests, resetAt)
			return true, false
		}
		writeResponseHeaders(c, resp)
		_, _ = c.Writer.Write(errBody)
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.ProviderOpenAI,
			AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
			Stream: stream, Path: path, Status: resp.StatusCode, Attempts: attempts,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      fmt.Sprintf("upstream %d", resp.StatusCode),
		})
		return false, true
	}

	var counts usage.Counts
	switch {
	case isCompactPath:
		// /codex/responses/compact returns a single JSON object — no SSE.
		// Read it once, extract usage, pass through verbatim. Matches sub2api's
		// handleNonStreamingResponsePassthrough behavior on this path.
		payload, rerr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if rerr != nil {
			log.Warnf("codex oauth: read compact body via %s: %v", a.ID, rerr)
			c.AbortWithStatusJSON(502, gin.H{"error": "codex upstream: " + rerr.Error()})
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.ProviderOpenAI,
				AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
				Stream: stream, Path: path, Status: 502, Attempts: attempts,
				DurationMs: time.Since(start).Milliseconds(),
				Error:      rerr.Error(),
			})
			return false, true
		}
		counts.Add(extractCodexBackendUsageFromJSON(payload))
		// Drop hop-by-hop / encoding headers; we've already consumed and may
		// be sending different bytes than the upstream advertised.
		for k, v := range resp.Header {
			if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") || strings.EqualFold(k, "Content-Encoding") {
				continue
			}
			for _, val := range v {
				c.Writer.Header().Add(k, val)
			}
		}
		c.Writer.Header().Set("Content-Type", "application/json")
		c.Writer.WriteHeader(resp.StatusCode)
		_, _ = c.Writer.Write(payload)
	case stream:
		// Streaming client: passthrough SSE verbatim.
		writeResponseHeaders(c, resp)
		streamSSECodexBackend(c, resp, &counts)
	default:
		// Non-streaming client: aggregate SSE into a single response object
		// (mirrors CLIProxyAPI's CodexExecutor.Execute aggregation).
		payload, aerr := aggregateCodexResponseStream(resp.Body, &counts)
		if aerr != nil {
			log.Warnf("codex oauth: aggregation via %s failed: %v", a.ID, aerr)
			c.AbortWithStatusJSON(502, gin.H{"error": "codex upstream: " + aerr.Error()})
			_ = resp.Body.Close()
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.ProviderOpenAI,
				AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
				Stream: stream, Path: path, Status: 502, Attempts: attempts,
				DurationMs: time.Since(start).Milliseconds(),
				Error:      aerr.Error(),
			})
			return false, true
		}
		// Drop the upstream's Content-Type: we're returning JSON, not SSE.
		for k, v := range resp.Header {
			if strings.EqualFold(k, "Content-Type") || strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
				continue
			}
			for _, val := range v {
				c.Writer.Header().Add(k, val)
			}
		}
		c.Writer.Header().Set("Content-Type", "application/json")
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write(payload)
	}
	_ = resp.Body.Close()

	s.usage.Record(a.ID, a.Label, counts)
	var costUSD float64
	var userID int64
	var multiplier float64
	if resp.StatusCode < 400 && counts.Requests > 0 && clientToken != "" {
		official := s.pricing.Cost(auth.ProviderOpenAI, model, counts)
		costUSD = official
		// Same single-funnel as Anthropic: hand the official cost to the
		// SaaS adapter, get back the billed amount, log/charge with it.
		if info, ok := saasInfoFrom(c); ok && s.saas != nil {
			billed, err := s.saas.Charge(c.Request.Context(), info, auth.ProviderOpenAI, model, counts, official)
			if err != nil {
				log.Warnf("saas: charge failed for token=%d user=%d: %v", info.TokenID, info.UserID, err)
			} else {
				costUSD = billed
				userID = info.UserID
				multiplier = s.saas.MultiplierFor(c.Request.Context(), info.GroupID, auth.ProviderOpenAI)
			}
		}
		s.usage.RecordClient(clientToken, clientName, counts, costUSD)
	}
	s.emitLog(requestlog.Record{
		Client:      clientName,
		ClientToken: maskClientToken(clientToken),
		Provider:    auth.ProviderOpenAI,
		AuthID:      a.ID,
		AuthLabel:   a.Label,
		AuthKind:    "oauth",
		Model:       model,
		Input:       counts.InputTokens,
		Output:      counts.OutputTokens,
		CacheRead:   counts.CacheReadTokens,
		CostUSD:     costUSD,
		UserID:      userID,
		Multiplier:  multiplier,
		Status:      resp.StatusCode,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      stream,
		Path:        path,
		Attempts:    attempts,
	})
	if resp.StatusCode < 400 {
		a.MarkSuccess()
	}
	return false, true
}

// aggregateCodexResponseStream reads the backend SSE stream and returns
// the final response JSON object for a non-streaming client. Mirrors the
// aggregation in CLIProxyAPI's CodexExecutor.Execute: collects
// `response.output_item.done` items (keyed by output_index when present,
// falling back to arrival order), then on `response.completed` patches
// the response.output field if it arrived empty. Output shape matches
// OpenAI's /v1/responses non-streaming reply: the bare `response` object
// (id, object, output, usage, …) — not the SSE event envelope.
func aggregateCodexResponseStream(r io.Reader, counts *usage.Counts) ([]byte, error) {
	reader := newLineReader(r)
	var byIndex []codexOutputSlot
	var fallback []json.RawMessage

	for {
		line, rerr := reader.readLine()
		if len(line) > 0 {
			trim := bytes.TrimRight(line, "\r\n")
			if bytes.HasPrefix(trim, []byte("data:")) {
				payload := bytes.TrimSpace(trim[5:])
				if len(payload) > 0 && payload[0] == '{' {
					var ev struct {
						Type        string          `json:"type"`
						Item        json.RawMessage `json:"item"`
						OutputIndex *int64          `json:"output_index"`
						Response    json.RawMessage `json:"response"`
					}
					if err := json.Unmarshal(payload, &ev); err == nil {
						switch ev.Type {
						case "response.output_item.done":
							if len(ev.Item) > 0 {
								if ev.OutputIndex != nil {
									byIndex = append(byIndex, codexOutputSlot{idx: *ev.OutputIndex, data: ev.Item})
								} else {
									fallback = append(fallback, ev.Item)
								}
							}
						case "response.completed":
							if len(ev.Response) == 0 {
								return nil, errors.New("response.completed missing response field")
							}
							counts.Add(extractCodexBackendUsageFromJSON(payload))
							return patchResponseOutput(ev.Response, byIndex, fallback)
						}
					}
				}
			}
		}
		if rerr != nil {
			return nil, fmt.Errorf("stream closed before response.completed: %w", rerr)
		}
	}
}

// patchResponseOutput replaces response.output with the collected
// output_item.done events when the completed event arrived with an empty
// or missing output array. Returns the (possibly unchanged) response JSON.
type codexOutputSlot struct {
	idx  int64
	data json.RawMessage
}

func patchResponseOutput(response json.RawMessage, byIndex []codexOutputSlot, fallback []json.RawMessage) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(response, &obj); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	// Only patch if the existing output is missing or empty.
	needsPatch := true
	if cur, ok := obj["output"]; ok {
		t := bytes.TrimSpace(cur)
		if len(t) > 2 && !bytes.Equal(t, []byte("[]")) && !bytes.Equal(t, []byte("null")) {
			needsPatch = false
		}
	}
	if needsPatch && (len(byIndex) > 0 || len(fallback) > 0) {
		sort.SliceStable(byIndex, func(i, j int) bool { return byIndex[i].idx < byIndex[j].idx })
		items := make([]json.RawMessage, 0, len(byIndex)+len(fallback))
		for _, s := range byIndex {
			items = append(items, s.data)
		}
		items = append(items, fallback...)
		patched, err := json.Marshal(items)
		if err != nil {
			return nil, err
		}
		obj["output"] = patched
	}
	return json.Marshal(obj)
}

// streamSSECodexBackend is the Codex backend SSE passthrough. The format
// differs from OpenAI's API-key response: events carry JSON payloads
// structured as `response.completed` / `response.output_item.done` etc.
// Usage arrives inside the `response.completed` event as
// `response.usage.{input_tokens, output_tokens, input_tokens_details.cached_tokens}`.
func streamSSECodexBackend(c *gin.Context, resp *http.Response, counts *usage.Counts) {
	flusher, _ := c.Writer.(http.Flusher)
	reader := newLineReader(resp.Body)
	for {
		line, err := reader.readLine()
		if len(line) > 0 {
			trim := bytes.TrimRight(line, "\r\n")
			if bytes.HasPrefix(trim, []byte("data:")) {
				payload := bytes.TrimSpace(trim[5:])
				if len(payload) > 0 && payload[0] == '{' {
					counts.Add(extractCodexBackendUsageFromJSON(payload))
				}
			}
			_, _ = c.Writer.Write(line)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
}

// extractCodexBackendUsageFromJSON reads usage from the ChatGPT Codex
// backend's response/event JSON, covering both shapes:
//
//	{"response":{"usage":{...}}}        ← streaming "response.completed"
//	{"usage":{...}}                     ← non-stream compact wrapper
//
// Cached input tokens are split out into Counts.CacheReadTokens so they're
// billed at the discounted rate.
func extractCodexBackendUsageFromJSON(body []byte) usage.Counts {
	if len(body) == 0 {
		return usage.Counts{}
	}
	var wrap struct {
		Response struct {
			Usage *openaiUsage `json:"usage"`
		} `json:"response"`
		Usage *openaiUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return usage.Counts{}
	}
	u := wrap.Response.Usage
	if u == nil {
		u = wrap.Usage
	}
	if u == nil {
		return usage.Counts{}
	}
	return u.toCounts()
}

// isCodexCapacityError detects the upstream's "model is at capacity"
// rejection so the picker cools down this credential without giving up on
// the request. Strings come from CLIProxyAPI's codex_executor.go.
func isCodexCapacityError(body []byte) bool {
	lower := bytes.ToLower(body)
	return bytes.Contains(lower, []byte("selected model is at capacity")) ||
		bytes.Contains(lower, []byte("model is at capacity"))
}

// parseCodexResetAt extracts the reset timestamp from a usage_limit_reached
// error body. Supports both epoch-seconds and relative-seconds encodings:
//
//	{"error":{"type":"usage_limit_reached","resets_at":1716000000}}
//	{"error":{"type":"usage_limit_reached","resets_in_seconds":3600}}
func parseCodexResetAt(body []byte) time.Time {
	if len(body) == 0 {
		return time.Time{}
	}
	var wrap struct {
		Error struct {
			Type            string  `json:"type"`
			ResetsAt        int64   `json:"resets_at"`
			ResetsInSeconds float64 `json:"resets_in_seconds"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return time.Time{}
	}
	if wrap.Error.ResetsAt > 0 {
		return time.Unix(wrap.Error.ResetsAt, 0)
	}
	if wrap.Error.ResetsInSeconds > 0 {
		return time.Now().Add(time.Duration(wrap.Error.ResetsInSeconds) * time.Second)
	}
	return time.Time{}
}

// lineReader is a tiny buffered reader that preserves the original
// trailing newline so the passthrough writes the exact bytes the upstream
// sent (SSE is whitespace-sensitive).
type lineReader struct {
	buf []byte
	pos int
	src io.Reader
}

func newLineReader(r io.Reader) *lineReader { return &lineReader{src: r, buf: make([]byte, 0, 8192)} }

func (lr *lineReader) readLine() ([]byte, error) {
	for {
		if idx := bytes.IndexByte(lr.buf[lr.pos:], '\n'); idx >= 0 {
			line := lr.buf[lr.pos : lr.pos+idx+1]
			lr.pos += idx + 1
			if lr.pos >= len(lr.buf) {
				lr.buf = lr.buf[:0]
				lr.pos = 0
			}
			return line, nil
		}
		// Shift remaining unread bytes to the start before the next read
		// so we don't grow the buffer unbounded on a slow stream.
		if lr.pos > 0 {
			copy(lr.buf, lr.buf[lr.pos:])
			lr.buf = lr.buf[:len(lr.buf)-lr.pos]
			lr.pos = 0
		}
		chunk := make([]byte, 4096)
		n, err := lr.src.Read(chunk)
		if n > 0 {
			lr.buf = append(lr.buf, chunk[:n]...)
		}
		if err != nil {
			// Flush any tail bytes without a terminator on EOF.
			if lr.pos < len(lr.buf) {
				rest := lr.buf[lr.pos:]
				lr.pos = len(lr.buf)
				return rest, err
			}
			return nil, err
		}
	}
}
