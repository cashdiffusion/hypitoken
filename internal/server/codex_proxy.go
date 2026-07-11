package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/usage"
)

// Codex / OpenAI endpoint handlers. The request/retry/accounting machinery
// lives in forward() (proxy.go); this file supplies the provider-specific
// upstream call (doForwardCodex) plus the Codex-native route handlers.
//
// This file implements the API-key path — requests are forwarded to
// api.openai.com (or an overridden base URL) with the credential's bearer
// key swapped in. The OAuth path lives in doForwardCodexOAuth
// (codex_oauth_proxy.go) so the request-transformation complexity doesn't
// clutter the BYOK flow.

func (s *Server) handleCodexChatCompletions(c *gin.Context) {
	s.forward(c, auth.ProviderOpenAI, "/v1/chat/completions")
}

func (s *Server) handleCodexResponses(c *gin.Context) {
	s.forward(c, auth.ProviderOpenAI, "/v1/responses")
}

// handleCodexResponsesCompact forwards the Codex CLI's conversation-compaction
// request. Same /v1/responses body shape, different upstream path
// (/codex/responses/compact on the ChatGPT backend; /v1/responses/compact
// on API-key relays). Routed to the same forward() machinery — the path is
// translated at the upstream-call layer.
func (s *Server) handleCodexResponsesCompact(c *gin.Context) {
	s.forward(c, auth.ProviderOpenAI, "/v1/responses/compact")
}

// handleCodexModels returns the union of models exposed by the loaded
// OpenAI credentials: OAuth creds contribute their plan-tier catalog
// (see auth.CodexModelsForPlan) and API-key creds contribute the
// upstream's authoritative /v1/models listing. Returned shape matches
// OpenAI's: {"object":"list","data":[{id, object, owned_by}, ...]}.
func (s *Server) handleCodexModels(c *gin.Context) {
	seen := map[string]bool{}
	var data []gin.H

	// OAuth: synthesize from plan_type claims so subscribers see exactly
	// the models their tier is entitled to (matches Codex CLI behavior).
	var apiKeyCred *auth.Auth
	for _, st := range s.pool.Status() {
		if auth.NormalizeProvider(st.Auth.Provider) != auth.ProviderOpenAI {
			continue
		}
		if st.Auth.Disabled {
			continue
		}
		live := s.pool.FindByID(st.Auth.ID)
		if live == nil {
			continue
		}
		if st.Auth.Kind == auth.KindOAuth {
			_, plan := live.CodexIdentity()
			for _, m := range auth.CodexModelsForPlan(plan) {
				if seen[m] {
					continue
				}
				seen[m] = true
				data = append(data, gin.H{"id": m, "object": "model", "owned_by": "openai"})
			}
			continue
		}
		if apiKeyCred == nil {
			apiKeyCred = live
		}
	}

	// API-key: transparent forward to upstream so BYOK users see whatever
	// their key is entitled to. Merge into `seen` so a model shared across
	// credentials isn't listed twice.
	if apiKeyCred != nil {
		if upstream, err := s.fetchCodexAPIKeyModels(c.Request.Context(), apiKeyCred); err == nil {
			for _, m := range upstream {
				if seen[m.id] {
					continue
				}
				seen[m.id] = true
				data = append(data, gin.H{"id": m.id, "object": "model", "owned_by": m.ownedBy})
			}
		} else {
			log.Warnf("codex: /v1/models upstream probe via %s failed: %v", apiKeyCred.ID, err)
		}
	}

	if data == nil {
		data = []gin.H{}
	}
	c.JSON(200, gin.H{"object": "list", "data": data})
}

type codexUpstreamModel struct{ id, ownedBy string }

func (s *Server) fetchCodexAPIKeyModels(ctx context.Context, a *auth.Auth) ([]codexUpstreamModel, error) {
	snap := a.Snapshot()
	baseURL := strings.TrimRight(snap.BaseURL, "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(s.cfg.OpenAIBaseURL, "/")
	}
	// Shared join rule (see mimicry.JoinCodexAPIKeyUpstreamURL): a bare-origin
	// relay BaseURL keeps /v1 (new-api/one-api serve /v1/models); a BaseURL
	// that already carries a path is authoritative.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mimicry.JoinCodexAPIKeyUpstreamURL(baseURL, "/v1/models"), nil)
	if err != nil {
		return nil, err
	}
	access, _ := a.Credentials()
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Accept", "application/json")
	client := auth.ClientFor(snap.ProxyURL, s.cfg.UseUTLS)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(truncate(body, 200))
	}
	var wrap struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, err
	}
	out := make([]codexUpstreamModel, 0, len(wrap.Data))
	for _, m := range wrap.Data {
		out = append(out, codexUpstreamModel{id: m.ID, ownedBy: m.OwnedBy})
	}
	return out, nil
}

// doForwardCodex performs one upstream attempt against an OpenAI-style
// provider credential. Contract matches doForward (proxy.go):
//
//	retry=true  → caller should exclude this credential and retry
//	done=true   → response was delivered (success or non-retryable error)
//
// Only API-key credentials are handled here; OAuth credentials are
// delegated to doForwardCodexOAuth (codex_oauth_proxy.go), a full
// implementation that forwards to the ChatGPT Codex backend.
func (s *Server) doForwardCodex(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, clientName string, start time.Time, attempts int) (retry, done bool) {
	if a.Kind == auth.KindOAuth {
		return s.doForwardCodexOAuth(c, a, path, body, stream, model, clientToken, clientName, start, attempts)
	}

	// API-key passthrough. We do not inject any Codex-CLI mimicry, do not
	// use uTLS, and do not normalize the request body (compact whitelist /
	// stream_options injection). The only allowed request-side change is the
	// per-credential model rewrite (and matching response-side rewrite) so
	// model_map'd relay vendors keep working.
	//
	// Health tracking: success → MarkSuccess, 401/403 → MarkHardFailure
	// (sticky Unhealthy in admin), and these forward to the client verbatim.
	// Retryable upstream failures — 429, 5xx, and transport/gateway errors,
	// the signature of a relay vendor that's down or throttling — are NOT
	// relayed: we roll back to the forward loop (retry=true) so it excludes
	// this credential and tries the next, and briefly cool the bad relay down
	// so it stops being picked. This is what keeps one dead relay (e.g. a
	// reseller returning a 502 page) from surfacing 502s to every client when
	// healthy Codex credentials are available. See cooldownCodexAPIKey.
	snap := a.Snapshot()
	baseURL := strings.TrimRight(snap.BaseURL, "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(s.cfg.OpenAIBaseURL, "/")
	}
	// Join BaseURL with the inbound endpoint via the shared rule
	// (mimicry.JoinCodexAPIKeyUpstreamURL):
	//   BaseURL=https://api.openai.com/v1 + /v1/responses → .../v1/responses ✓
	//   BaseURL=https://relay.example     + /v1/responses → .../v1/responses ✓ (bare origin keeps /v1)
	//   BaseURL=https://gateway.io/codex  + /v1/responses → .../codex/responses ✓
	// A bare-origin relay (new-api/one-api) serves under /v1, so we no longer
	// strip it into a /responses request that hit the gateway HTML homepage and
	// surfaced as "stream disconnected before completion".
	upURL := mimicry.JoinCodexAPIKeyUpstreamURL(baseURL, path)

	upstreamBody := body
	rewriteClientModel := ""
	if stream {
		if rewritten, err := usage.EnsureOpenAIStreamUsage(upstreamBody); err == nil {
			upstreamBody = rewritten
		} else {
			log.Warnf("codex proxy(apikey): stream usage injection skipped for non-JSON body via %s: %v", a.ID, err)
		}
	}
	if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		if rewritten, err := rewriteModelField(upstreamBody, upstreamModel); err == nil {
			upstreamBody = rewritten
			rewriteClientModel = model
		} else {
			log.Warnf("codex proxy(apikey): model rewrite (%s -> %s) failed via %s: %v", model, upstreamModel, a.ID, err)
		}
	}

	ctx := c.Request.Context()
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upURL, bytes.NewReader(upstreamBody))
	if err != nil {
		writeAPIError(c, auth.ProviderOpenAI, APIError{Status: http.StatusInternalServerError, Code: "request_preparation_failed", Message: "The request could not be prepared. Please try again."})
		return false, true
	}
	copyForwardableHeaders(c.Request.Header, upReq.Header)
	stripIngressHeaders(upReq.Header)
	accessToken, _ := a.Credentials()
	upReq.Header.Set("Authorization", "Bearer "+accessToken)

	client := auth.ClientFor(snap.ProxyURL, false)
	resp, err := client.Do(upReq)
	if err != nil {
		if isClientDisconnect(ctx, err) {
			log.Infof("codex proxy(apikey): client canceled via %s: %v", a.ID, err)
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken),
				Provider: auth.ProviderOpenAI, AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
				Model: model, Stream: stream, Path: path, Status: 499,
				DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: "client canceled",
			})
			return false, true
		}
		// Transport/gateway failure — treat like a retryable 5xx: cool the
		// credential down and roll back to the loop to try the next one
		// instead of handing the client a bare 502.
		log.Warnf("codex proxy(apikey): upstream transport error via %s: %v — rotating to next credential", a.ID, err)
		s.cooldownCodexAPIKey(a, http.StatusBadGateway, time.Time{})
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken),
			Provider: auth.ProviderOpenAI, AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
			Model: model, Stream: stream, Path: path, Status: 502,
			DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: err.Error(),
		})
		return true, false
	}

	// Retryable upstream failure (throttle / overload / gateway down): don't
	// relay it. Read+discard the body, cool the relay down, and roll back to
	// the loop to try the next credential. Nothing has been written to the
	// client yet, so the retry is transparent.
	if isCodexRetryableStatus(resp.StatusCode) {
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		snippet := truncate(errBody, 500)
		log.Warnf("codex proxy(apikey): %s returned %d — rotating to next credential. body=%s", a.ID, resp.StatusCode, snippet)
		s.cooldownCodexAPIKey(a, resp.StatusCode, parseRetryAfter(resp.Header))
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken),
			Provider: auth.ProviderOpenAI, AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
			Model: model, Stream: stream, Path: path, Status: resp.StatusCode,
			DurationMs: time.Since(start).Milliseconds(), Attempts: attempts,
			Error: fmt.Sprintf("upstream %d: %s", resp.StatusCode, truncate(errBody, 200)),
		})
		return true, false
	}

	var counts usage.Counts
	var errSnippet string
	var missingUsage bool
	switch {
	case resp.StatusCode >= 400:
		errBody, _ := io.ReadAll(resp.Body)
		errSnippet = truncate(errBody, 500)
		log.Warnf("codex proxy(apikey): %s returned %d — body=%s", a.ID, resp.StatusCode, errSnippet)
		copySafeRetryHeaders(c, resp.Header)
		writeAPIError(c, auth.ProviderOpenAI, publicUpstreamError(resp.StatusCode, errBody))
	default:
		// Decide SSE-vs-JSON from the client's stream flag + the actual bytes,
		// NOT the upstream Content-Type alone: relays (e.g. New-API gateways)
		// stream the /v1/responses SSE back as `text/plain`, which used to fall
		// through to the whole-body JSON parse and silently lose usage (billing
		// = $0). Mirrors the OAuth path and sub2api, which both dispatch on the
		// requested stream rather than the response header.
		br := bufio.NewReaderSize(resp.Body, 64*1024)
		if stream && responseIsSSE(resp.Header, br) {
			writeResponseHeaders(c, resp)
			streamSSEOpenAI(c, br, &counts, rewriteClientModel)
			if usage.MissingUsage(counts) {
				missingUsage = true
				counts = usage.MissingUsageFallbackCounts(body)
				log.Warnf("codex proxy(apikey): %s streamed success without usage; applying fallback charge and cooling credential", a.ID)
				s.cooldownCodexAPIKey(a, http.StatusBadGateway, time.Time{})
			}
		} else {
			respBody, _ := io.ReadAll(br)
			if rewriteClientModel != "" {
				respBody = rewriteResponseModel(respBody, rewriteClientModel)
			}
			parsed := extractOpenAIUsageFromJSON(respBody)
			if usage.MissingUsage(parsed) {
				_ = resp.Body.Close()
				log.Warnf("codex proxy(apikey): %s returned success without usage on non-stream response; failing closed", a.ID)
				s.cooldownCodexAPIKey(a, http.StatusBadGateway, time.Time{})
				writeAPIError(c, auth.ProviderOpenAI, APIError{Status: http.StatusBadGateway, Code: "service_response_error", Message: "The model service returned an incomplete response. Please try again."})
				s.emitLog(requestlog.Record{
					Client: clientName, ClientToken: maskClientToken(clientToken),
					Provider: auth.ProviderOpenAI, AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
					Model: model, Stream: stream, Path: path, Status: http.StatusBadGateway,
					DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: usage.MissingUsageError,
				})
				return false, true
			}
			writeResponseHeaders(c, resp)
			_, _ = c.Writer.Write(respBody)
			counts.Add(parsed)
		}
	}
	_ = resp.Body.Close()

	switch {
	case resp.StatusCode < 400 && !missingUsage:
		a.MarkSuccess()
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		a.MarkHardFailure(fmt.Sprintf("upstream %d", resp.StatusCode))
	}

	var costUSD float64
	var userID int64
	var multiplier float64
	if resp.StatusCode < 400 {
		s.usage.Record(a.ID, a.Label, counts)
		if counts.Requests > 0 && clientToken != "" {
			official := s.pricing.Cost(auth.ProviderOpenAI, model, counts)
			costUSD = official
			// Codex apikey path used to skip the SaaS Charge funnel entirely,
			// so wallets weren't deducted for Codex traffic. Route it through
			// the same path Anthropic uses now.
			if info, ok := saasInfoFrom(c); ok && s.saas != nil {
				billed, err := s.saas.Charge(c.Request.Context(), info, auth.ProviderOpenAI, model, counts, official)
				if err != nil {
					log.Warnf("saas: charge failed for token=%d user=%d: %v", info.TokenID, info.UserID, err)
				} else {
					costUSD = billed
					userID = info.UserID
					multiplier = s.saas.MultiplierFor(info, auth.ProviderOpenAI)
				}
			}
			s.usage.RecordClient(clientToken, clientName, counts, costUSD)
		}
	}
	errField := ""
	if resp.StatusCode >= 400 {
		errField = fmt.Sprintf("upstream %d: %s", resp.StatusCode, truncate([]byte(errSnippet), 200))
	} else if missingUsage {
		errField = usage.MissingUsageError
	}
	s.emitLog(requestlog.Record{
		Client:      clientName,
		ClientToken: maskClientToken(clientToken),
		Provider:    auth.ProviderOpenAI,
		AuthID:      a.ID,
		AuthLabel:   a.Label,
		AuthKind:    "apikey",
		Model:       model,
		Input:       counts.InputTokens,
		Output:      counts.OutputTokens,
		CacheRead:   counts.CacheReadTokens,
		CacheCreate: counts.CacheCreateTokens,
		UserID:      userID,
		Multiplier:  multiplier,
		CostUSD:     costUSD,
		Status:      resp.StatusCode,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      stream,
		Path:        path,
		Attempts:    attempts,
		Error:       errField,
	})
	return false, true
}

// codexAPIKey5xxCooldown is how long a Codex API-key relay is taken out of
// rotation after a 5xx / gateway failure. Short on purpose: long enough to
// cover the in-request retry sweep and the immediately-following requests so
// a dead relay stops serving errors, short enough that a relay which recovers
// is picked back up within a minute without manual intervention. 429s use the
// pool's own growing backoff instead (which honors Retry-After).
const codexAPIKey5xxCooldown = 45 * time.Second

// isCodexRetryableStatus reports whether an upstream status from a Codex
// API-key relay should be retried on a different credential rather than
// forwarded to the client. Throttling (429) and server/gateway failures
// (5xx) are the transient classes worth rotating away from; every other 4xx
// (400/401/403/404/422 …) is the client's or the credential's own answer and
// is forwarded verbatim.
func isCodexRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// cooldownCodexAPIKey records a retryable upstream failure on a Codex API-key
// relay and takes it out of rotation so it stops being selected while broken.
// A 429 goes through the pool's normal 429 path (growing backoff, Retry-After
// aware); a 5xx / transport failure gets MarkFailure (admin-visible counter)
// plus a short fixed cooldown — ReportUpstreamError deliberately applies no
// cooldown for 5xx on API keys, which is exactly what let a persistently-dead
// relay keep getting picked.
func (s *Server) cooldownCodexAPIKey(a *auth.Auth, status int, resetAt time.Time) {
	if status == http.StatusTooManyRequests {
		s.pool.ReportUpstreamError(a, status, resetAt)
		return
	}
	a.MarkFailure(fmt.Sprintf("upstream %d", status))
	a.MarkQuotaExceeded(time.Now().Add(codexAPIKey5xxCooldown))
}

// responseIsSSE reports whether a <400 response should be parsed as an SSE
// stream. It trusts the Content-Type when it advertises `text/event-stream`,
// but also peeks the buffered body for a `data:`/`event:` line — some relays
// stream the Codex /v1/responses SSE back as `text/plain` (no event-stream
// header), and a header-only check would lose their usage. Peek does not
// consume, so the same reader is safe to hand to streamSSEOpenAI afterward.
func responseIsSSE(h http.Header, br *bufio.Reader) bool {
	if strings.Contains(h.Get("Content-Type"), "text/event-stream") {
		return true
	}
	return looksLikeSSE(br)
}

// looksLikeSSE peeks the first chunk of a buffered reader and reports whether
// it begins with an SSE field line (`data:` / `event:`), tolerating leading
// blank lines. Non-consuming.
func looksLikeSSE(br *bufio.Reader) bool {
	peek, _ := br.Peek(512)
	for len(peek) > 0 {
		nl := bytes.IndexByte(peek, '\n')
		var line []byte
		if nl < 0 {
			line = peek
			peek = nil
		} else {
			line = peek[:nl]
			peek = peek[nl+1:]
		}
		line = bytes.TrimRight(line, "\r")
		if len(line) == 0 {
			continue // skip leading blank lines
		}
		return bytes.HasPrefix(line, []byte("data:")) || bytes.HasPrefix(line, []byte("event:"))
	}
	return false
}

// streamSSEOpenAI is the OpenAI SSE passthrough. The wire format is `data:
// <json>\n\n` with a terminal `data: [DONE]`. Usage arrives in the final
// chunk when stream_options.include_usage is on (we always ensure that);
// parsing it here keeps billing correct for streaming clients.
func streamSSEOpenAI(c *gin.Context, reader *bufio.Reader, counts *usage.Counts, rewriteClientModel string) {
	flusher, _ := c.Writer.(http.Flusher)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			trim := bytes.TrimRight(line, "\r\n")
			outLine := line
			if bytes.HasPrefix(trim, []byte("data:")) {
				payload := bytes.TrimSpace(trim[5:])
				// Skip the DONE sentinel and non-JSON payloads.
				if len(payload) > 0 && payload[0] == '{' {
					if rewriteClientModel != "" {
						if rewritten := rewriteResponseModel(payload, rewriteClientModel); rewritten != nil {
							tail := line[len(trim):]
							rebuilt := make([]byte, 0, len("data: ")+len(rewritten)+len(tail))
							rebuilt = append(rebuilt, []byte("data: ")...)
							rebuilt = append(rebuilt, rewritten...)
							rebuilt = append(rebuilt, tail...)
							outLine = rebuilt
						}
					}
					counts.Add(extractOpenAIUsageFromJSON(payload))
				}
			}
			_, _ = c.Writer.Write(outLine)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
}

// extractOpenAIUsageFromJSON pulls a usage.Counts from an OpenAI-shaped
// response chunk. Handles both the /v1/chat/completions shape:
//
//	{"usage":{"prompt_tokens":N,"completion_tokens":M,
//	  "prompt_tokens_details":{"cached_tokens":K}}}
//
// and the /v1/responses shape (nested under "response.usage" when wrapped
// in an event envelope, or top-level):
//
//	{"response":{"usage":{"input_tokens":N,"output_tokens":M,
//	  "input_tokens_details":{"cached_tokens":K}}}}
//
// Returns a zero Counts when no usage is present — the caller Adds them so
// absent usage is idempotent. Requests counter is incremented only when
// non-zero token counts actually landed (mirrors Anthropic extractor).
func extractOpenAIUsageFromJSON(body []byte) usage.Counts {
	if len(body) == 0 {
		return usage.Counts{}
	}
	var wrap struct {
		Usage    *openaiUsage `json:"usage"`
		Response struct {
			Usage *openaiUsage `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return usage.Counts{}
	}
	u := wrap.Usage
	if u == nil {
		u = wrap.Response.Usage
	}
	if u == nil {
		return usage.Counts{}
	}
	return u.toCounts()
}

type openaiUsage struct {
	// chat/completions names
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	// /v1/responses names
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func (u openaiUsage) toCounts() usage.Counts {
	input := u.PromptTokens
	if input == 0 {
		input = u.InputTokens
	}
	output := u.CompletionTokens
	if output == 0 {
		output = u.OutputTokens
	}
	cached := u.PromptTokensDetails.CachedTokens
	if cached == 0 {
		cached = u.InputTokensDetails.CachedTokens
	}
	// Follow OpenAI billing: cached prompt tokens are billed at a discount,
	// so we split prompt_tokens into (input - cached) + cached.
	nonCached := input - cached
	if nonCached < 0 {
		nonCached = 0
	}
	// No request is counted unless we actually observed usage data — this
	// keeps partial-stream chunks from over-incrementing the request
	// counter.
	if input == 0 && output == 0 && cached == 0 {
		return usage.Counts{}
	}
	return usage.Counts{
		InputTokens:     nonCached,
		OutputTokens:    output,
		CacheReadTokens: cached,
		Requests:        1,
	}
}

// small helper duplicating what proxy.go expresses inline — kept separate
// so codex_proxy stays self-contained for future edits.
// isClientDisconnect reports whether err from an upstream request came from
// the *client* going away, not the upstream / proxy dropping the socket.
// Use `ctx` (the client's request context) as the discriminator: if our own
// context is canceled, the client is gone; otherwise the error happened on
// the wire between us and the upstream and should be retried on another
// credential, not masked as "client canceled".
//
// We still accept context.Canceled / DeadlineExceeded *when the ctx has a
// matching error* — http.Client.Do sometimes wraps proxy-side resets in
// context.Canceled after an internal timeout, and those we want to retry.
func isClientDisconnect(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	// Fall-through: a raw context.Canceled with no ctx cancel means the
	// transport itself aborted — treat as upstream failure, not client cancel.
	return false
}

// isTransientNetErr reports whether err looks like a transient wire-level
// failure worth a short retry on the same credential. Targets the CF
// new-connection rate-limit symptom on chatgpt.com (RST mid-TLS), h2 stream
// rejections (PROTOCOL_ERROR / REFUSED_STREAM), and similar proxy/h2 flaps.
// Distinct from isClientDisconnect (client went away) and from HTTP-status
// errors (handled by the pool's ReportUpstreamError path).
//
// Delegates to the canonical classifier in cc-core (auth.IsTransientNetErr) so
// the transport's backoff-retry layer and this caller-side "defer to another
// credential without MarkFailure" decision stay in lockstep.
func isTransientNetErr(err error) bool {
	return auth.IsTransientNetErr(err)
}
