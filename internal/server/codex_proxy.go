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
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/usage"
)

// Codex / OpenAI endpoint handlers. The request/retry/accounting machinery
// lives in forward() (proxy.go); this file supplies the provider-specific
// upstream call (doForwardCodex) plus the Codex-native route handlers.
//
// This phase implements the API-key path — requests are forwarded to
// api.openai.com (or an overridden base URL) with the credential's bearer
// key swapped in. The OAuth path lands in phase 5 via a separate helper so
// the request-transformation complexity doesn't clutter the BYOK flow.

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
	// Same authoritative-BaseURL rule as the chat/completions forwarder —
	// see the comment there. /models is the bare endpoint; the user's
	// BaseURL decides whether /v1/ comes before it.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
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
// Only API-key credentials are handled in this phase; OAuth credentials
// bypass to doForwardCodexOAuth (phase 5 — currently a 501 shim).
func (s *Server) doForwardCodex(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, clientName string, start time.Time, attempts int) (retry, done bool) {
	if a.Kind == auth.KindOAuth {
		return s.doForwardCodexOAuth(c, a, path, body, stream, model, clientToken, clientName, start, attempts)
	}

	// API-key passthrough. We do not inject any Codex-CLI mimicry, do not
	// use uTLS, do not normalize the request body (compact whitelist /
	// stream_options injection), and do not retry across credentials.
	// Whatever the upstream returns is forwarded to the client verbatim.
	// The only allowed request-side change is the per-credential model
	// rewrite (and matching response-side rewrite) so model_map'd relay
	// vendors keep working.
	//
	// Health tracking is intentionally minimal: success → MarkSuccess,
	// 401/403 → MarkHardFailure (sticky Unhealthy in admin). Transient
	// 5xx / 429 / network errors are NOT recorded — we don't want a brief
	// upstream flap to flip the credential into a "degraded" yellow state.
	snap := a.Snapshot()
	baseURL := strings.TrimRight(snap.BaseURL, "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(s.cfg.OpenAIBaseURL, "/")
	}
	// BaseURL is authoritative for the API version prefix. We treat the
	// inbound path's `/v1/` as canonical-OpenAI-routing only — we strip it
	// before joining so the upstream URL is exactly
	// `<configured BaseURL>/<endpoint>`. This means:
	//   BaseURL=https://api.openai.com/v1 + /v1/responses → .../v1/responses ✓
	//   BaseURL=https://tcdmx.com         + /v1/responses → .../responses    ✓
	//   BaseURL=https://gateway.io/codex  + /v1/responses → .../codex/responses
	// If a user wants /v1/ in the upstream URL, they must include it in
	// the BaseURL field on the credential — no auto-injection, no auto-stripping.
	upURL := baseURL + strings.TrimPrefix(path, "/v1")

	upstreamBody := body
	rewriteClientModel := ""
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
		c.AbortWithStatusJSON(500, gin.H{"error": err.Error()})
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
		log.Warnf("codex proxy(apikey): upstream transport error via %s: %v", a.ID, err)
		c.AbortWithStatusJSON(502, gin.H{"error": err.Error()})
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken),
			Provider: auth.ProviderOpenAI, AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
			Model: model, Stream: stream, Path: path, Status: 502,
			DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: err.Error(),
		})
		return false, true
	}

	writeResponseHeaders(c, resp)
	var counts usage.Counts
	var errSnippet string
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		c.Writer.Write(errBody)
		errSnippet = truncate(errBody, 500)
		log.Warnf("codex proxy(apikey): %s returned %d — body=%s", a.ID, resp.StatusCode, errSnippet)
	} else if stream && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		streamSSEOpenAI(c, resp, &counts, rewriteClientModel)
	} else {
		respBody, _ := io.ReadAll(resp.Body)
		if rewriteClientModel != "" {
			respBody = rewriteResponseModel(respBody, rewriteClientModel)
		}
		c.Writer.Write(respBody)
		counts.Add(extractOpenAIUsageFromJSON(respBody))
	}
	_ = resp.Body.Close()

	switch {
	case resp.StatusCode < 400:
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
					multiplier = s.saas.MultiplierFor(c.Request.Context(), info.GroupID, auth.ProviderOpenAI)
				}
			}
			s.usage.RecordClient(clientToken, clientName, counts, costUSD)
		}
	}
	errField := ""
	if resp.StatusCode >= 400 {
		errField = fmt.Sprintf("upstream %d: %s", resp.StatusCode, truncate([]byte(errSnippet), 200))
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

// ensureStreamOptionsIncludeUsage rewrites the JSON request body so that
// `stream_options.include_usage` is true unless the client already set it.
// Leaves non-JSON bodies untouched. No-op when the field is already present.
func ensureStreamOptionsIncludeUsage(body []byte) ([]byte, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body, err
	}
	opts, _ := raw["stream_options"].(map[string]any)
	if opts == nil {
		opts = map[string]any{}
	}
	if _, set := opts["include_usage"]; !set {
		opts["include_usage"] = true
	}
	raw["stream_options"] = opts
	out, err := json.Marshal(raw)
	if err != nil {
		return body, err
	}
	return out, nil
}

// streamSSEOpenAI is the OpenAI SSE passthrough. The wire format is `data:
// <json>\n\n` with a terminal `data: [DONE]`. Usage arrives in the final
// chunk when stream_options.include_usage is on (we always ensure that);
// parsing it here keeps billing correct for streaming clients.
func streamSSEOpenAI(c *gin.Context, resp *http.Response, counts *usage.Counts, rewriteClientModel string) {
	flusher, _ := c.Writer.(http.Flusher)
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
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
			c.Writer.Write(outLine)
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
// new-connection rate-limit symptom on chatgpt.com (RST mid-TLS) and similar
// proxy/h2 flaps. Distinct from isClientDisconnect (client went away) and
// from HTTP-status errors (handled by the pool's ReportUpstreamError path).
func isTransientNetErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, io.EOF) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "unexpected EOF") ||
		strings.Contains(s, "http2: server sent GOAWAY")
}
