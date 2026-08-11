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
	// Health tracking shares classifyUpstreamStatus with the Anthropic
	// API-key path (upstream_health.go). Retryable faults — 429, 5xx,
	// transport errors, and a rejected key — are NOT relayed: we roll back to
	// the forward loop (retry=true) so it excludes this credential and tries
	// the next, and report the fault so cc-core's breaker pauses the relay and
	// stops it being picked. This is what keeps one dead relay (e.g. a
	// reseller returning a 502 page) from surfacing 502s to every client when
	// healthy Codex credentials are available. Client-side faults (400, 404,
	// 422 …) are forwarded verbatim and leave health alone. See
	// reportCodexAPIKeyFault.
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
		s.reportCodexAPIKeyFault(a, http.StatusBadGateway, time.Time{})
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken),
			Provider: auth.ProviderOpenAI, AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
			Model: model, Stream: stream, Path: path, Status: 502,
			DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: err.Error(),
		})
		return true, false
	}

	// Retryable fault (throttle / overload / gateway down / this key rejected):
	// don't relay it. Read+discard the body, report the fault so the breaker
	// can pause the relay, and roll back to the loop to try the next
	// credential. Nothing has been written to the client yet, so the retry is
	// transparent.
	//
	// classifyUpstreamStatus is shared with the Anthropic API-key path, so the
	// two providers no longer disagree about which statuses are the client's
	// fault. It also widens the retry set here: 401/402/403 used to be
	// forwarded to the caller, but on a relay another key may well work, and a
	// rejected key needs to leave rotation rather than be re-presented on every
	// request.
	if classifyUpstreamStatus(resp.StatusCode).retryable() {
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		snippet := truncate(errBody, 500)
		log.Warnf("codex proxy(apikey): %s returned %d — rotating to next credential. body=%s", a.ID, resp.StatusCode, snippet)
		s.reportCodexAPIKeyFault(a, resp.StatusCode, parseRetryAfter(resp.Header))
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
	outcome := usage.StreamComplete
	// logStatus defaults to the upstream's. A mid-stream client hang-up
	// overrides it to 499 so a cancellation stops masquerading as a clean 200
	// in every "success rate" view.
	logStatus := resp.StatusCode
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
			writeSSEResponseHeaders(c, resp)
			clientGone := streamSSEOpenAI(c, br, &counts, rewriteClientModel)
			outcome = usage.ClassifyStreamOutcome(counts, clientGone)
			switch outcome {
			case usage.StreamClientCanceled:
				// The user walked away. Bill whatever usage arrived before they
				// did (often nothing) and leave the credential untouched — this
				// path used to charge a full-prompt estimate AND cool a healthy
				// key on every Ctrl-C.
				logStatus = 499
				a.MarkClientCancel(usage.ClientCanceledError)
				log.Infof("codex proxy(apikey): client canceled mid-stream via %s (observed in=%d out=%d)",
					a.ID, counts.InputTokens, counts.OutputTokens)
			case usage.StreamUpstreamNoUsage:
				// The relay served a stream it cannot account for. Bill nothing
				// — there is no honest number to put on the invoice — and cool
				// the credential so traffic rotates to one that reports usage.
				log.Warnf("codex proxy(apikey): %s streamed success without usage; billing $0 and cooling credential", a.ID)
				s.reportCodexAPIKeyFault(a, http.StatusBadGateway, time.Time{})
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
				s.reportCodexAPIKeyFault(a, http.StatusBadGateway, time.Time{})
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

	// Only success is recorded here. Every retryable fault — including
	// 401/402/403, which used to be marked at this point — returns above,
	// having already been reported to the breaker by reportCodexAPIKeyFault.
	// What reaches here with a >=400 status is a client-side fault (400, 404,
	// 422 …), which by design leaves credential health untouched.
	//
	// A client cancellation is a success for the credential: it served bytes
	// until the caller stopped listening. Only StreamUpstreamNoUsage withholds
	// MarkSuccess, and it has already reported the fault above.
	if resp.StatusCode < 400 && !outcome.CredentialFault() {
		a.MarkSuccess()
	}

	// CostUSD = official upstream price, BilledUSD = wallet debit.
	var costUSD, billedUSD float64
	var userID int64
	var multiplier float64
	if resp.StatusCode < 400 {
		s.usage.Record(a.ID, a.Label, counts)
		// outcome.Billable gates on OBSERVED usage — never on an estimate. A
		// stream the relay failed to account for costs the customer $0; the
		// credential's breaker, not the customer's wallet, absorbs it.
		if outcome.Billable(counts) && clientToken != "" {
			costUSD = s.pricing.Cost(auth.ProviderOpenAI, model, counts)
			billedUSD = costUSD
			// Codex apikey path used to skip the SaaS Charge funnel entirely,
			// so wallets weren't deducted for Codex traffic. Route it through
			// the same path Anthropic uses now.
			if info, ok := saasInfoFrom(c); ok && s.saas != nil {
				billed, err := s.saas.Charge(c.Request.Context(), info, auth.ProviderOpenAI, model, counts, costUSD)
				if err != nil {
					log.Warnf("saas: charge failed for token=%d user=%d: %v", info.TokenID, info.UserID, err)
				} else {
					billedUSD = billed
					userID = info.UserID
					multiplier = s.saas.MultiplierFor(info, auth.ProviderOpenAI)
				}
			}
			s.usage.RecordClient(clientToken, clientName, counts, billedUSD)
		}
	}
	errField := outcome.LogError()
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
		BilledUSD:   billedUSD,
		Status:      logStatus,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      stream,
		Path:        path,
		Attempts:    attempts,
		Error:       errField,
	})
	return false, true
}

// reportCodexAPIKeyFault records an upstream failure on a Codex API-key relay
// so the pool stops selecting it while it is broken.
//
// This used to hand a 5xx to MarkFailure *plus* a fixed 45s MarkQuotaExceeded.
// The quota call was a workaround, not an intent: MarkFailure alone could not
// take an API key out of rotation (IsHealthy skipped the consecutive-failure
// heuristic for KindAPIKey), and the quota cooldown was the only lever
// IsHealthy honoured — at the cost of reporting an upstream 5xx to operators
// as "quota exceeded", and of a flat interval that both over-reacted to a
// one-off 502 and under-reacted to a permanently dead relay by re-probing it
// every 45s forever.
//
// cc-core's API-key circuit breaker removes that constraint, so the classes
// now map onto shared machinery:
//
//	429            → the pool's throttling path (Retry-After aware, growing
//	                 backoff) — kept, because a rate limit is the one case
//	                 where the upstream tells us how long to wait
//	401/402/403    → MarkHardFailure: definitive, pauses on the first strike
//	5xx/transport/ → MarkFailure: pauses after a few in a row, then backs off
//	contract        exponentially and probes itself back in
//
// None of these retire the channel; every pause expires on its own.
func (s *Server) reportCodexAPIKeyFault(a *auth.Auth, status int, resetAt time.Time) {
	if status == http.StatusTooManyRequests {
		s.pool.ReportUpstreamError(a, status, resetAt)
		return
	}
	if classifyUpstreamStatus(status) == faultCredential {
		a.MarkHardFailure(fmt.Sprintf("upstream %d", status))
		return
	}
	a.MarkFailure(fmt.Sprintf("upstream %d", status))
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
// it begins with an SSE line, tolerating leading blank lines. Non-consuming.
//
// Every SSE line counts, not just `data:` / `event:`. A comment (`: …`) is the
// one that actually shows up first: an upstream emits comment keepalives while
// a turn is queued, and when it declares no Content-Type this peek is the whole
// decision. Accepting only field lines sent those turns through the whole-body
// JSON parse, which found no usage and failed them closed with a 502 —
// reproduced against a stub upstream that opens with `: queued`.
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
		return bytes.HasPrefix(line, []byte("data:")) ||
			bytes.HasPrefix(line, []byte("event:")) ||
			bytes.HasPrefix(line, []byte("id:")) ||
			bytes.HasPrefix(line, []byte("retry:")) ||
			bytes.HasPrefix(line, []byte(":"))
	}
	return false
}

// streamSSEOpenAI is the OpenAI SSE passthrough. The wire format is `data:
// <json>\n\n` with a terminal `data: [DONE]`. Usage arrives in the final
// chunk when stream_options.include_usage is on (we always ensure that);
// parsing it here keeps billing correct for streaming clients.
//
// It returns whether the CLIENT is what ended the stream. That distinction is
// load-bearing for billing: an upstream that never reported usage is a relay
// fault (bill nothing, cool the credential), while a client hang-up is the
// user's own choice (bill the partial usage, leave health alone). Conflating
// the two is what made every Ctrl-C both overcharge and trip the breaker.
//
// The read error alone cannot tell them apart — an upstream RST and a client
// disconnect surface identically at the reader — so the signal comes from the
// request context plus the write side, exactly as the Anthropic and
// Codex-OAuth paths already do.
func streamSSEOpenAI(c *gin.Context, reader *bufio.Reader, counts *usage.Counts, rewriteClientModel string) (clientGone bool) {
	flusher, _ := c.Writer.(http.Flusher)
	// c.Request is nil in unit tests that drive the relay directly; treat that
	// as "no cancellation signal" rather than panicking on the hot path.
	var ctx context.Context
	if c.Request != nil {
		ctx = c.Request.Context()
	}
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
			if _, werr := c.Writer.Write(outLine); werr != nil {
				// Writing to the client failed — it hung up. Nothing further can
				// be delivered, and the credential is blameless.
				return true
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			// A read error with our own context already canceled means the
			// client left and the transport tore the upstream down behind it.
			return ctx != nil && ctx.Err() != nil
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
