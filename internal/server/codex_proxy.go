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

	"github.com/wjsoj/cc-core/apicompat"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/codexerr"
	"github.com/wjsoj/cc-core/downstream"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
	ccstream "github.com/wjsoj/cc-core/stream"
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
	// Codex API-key providers are reached over the Responses protocol by
	// default: an inbound /v1/chat/completions is translated into a Responses
	// body and sent to the provider's /v1/responses, then rendered back as
	// chat.completion{,.chunk} on the way down. These relays resell Codex
	// capacity, which is Responses-native — several host /v1/responses only, so
	// forwarding chat/completions verbatim reached an endpoint that either
	// 404s or falls back to a different (non-Codex) upstream pool.
	//
	// The bridge is best-effort: a body apicompat declines to translate falls
	// back to verbatim chat/completions forwarding, which is what a plain
	// OpenAI-compatible gateway expects. Same translator as the OAuth path
	// (codex_oauth_proxy.go), so both credential kinds render identically.
	isChat := path == "/v1/chat/completions"
	bridged := false
	sourceBody := body
	upstreamPath := path
	if isChat {
		if converted, cerr := apicompat.ChatCompletionsToResponses(body); cerr == nil {
			sourceBody = converted
			upstreamPath = "/v1/responses"
			bridged = true
		} else {
			log.Infof("codex proxy(apikey): chat/completions bridge declined body via %s: %v — forwarding verbatim", a.ID, cerr)
		}
	}
	// Join BaseURL with the (possibly bridged) endpoint via the shared rule
	// (mimicry.JoinCodexAPIKeyUpstreamURL):
	//   BaseURL=https://api.openai.com/v1 + /v1/responses → .../v1/responses ✓
	//   BaseURL=https://relay.example     + /v1/responses → .../v1/responses ✓ (bare origin keeps /v1)
	//   BaseURL=https://gateway.io/codex  + /v1/responses → .../codex/responses ✓
	// A bare-origin relay (new-api/one-api) serves under /v1, so we no longer
	// strip it into a /responses request that hit the gateway HTML homepage and
	// surfaced as "stream disconnected before completion".
	upURL := mimicry.JoinCodexAPIKeyUpstreamURL(baseURL, upstreamPath)

	upstreamBody := sourceBody
	rewriteClientModel := ""
	// stream_options.include_usage is a Chat Completions field; Responses
	// reports usage on response.completed unconditionally. Injecting it into a
	// bridged body would get rejected as an unknown parameter, so it stays on
	// the verbatim path only. A bridged body instead carries the client's
	// stream intent explicitly, since the translator does not set it.
	if bridged {
		if rewritten, err := setJSONBool(upstreamBody, "stream", stream); err == nil {
			upstreamBody = rewritten
		} else {
			log.Warnf("codex proxy(apikey): stream flag injection failed on bridged body via %s: %v", a.ID, err)
		}
	} else if stream {
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
	// streamTruncated records an upstream that stopped without a terminal
	// event, so the request log names it instead of showing a clean 200 for a
	// stream the client saw break.
	var streamTruncated bool
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
		switch {
		case stream && responseIsSSE(resp.Header, br):
			writeSSEResponseHeaders(c, resp)
			var clientGone bool
			if bridged {
				// Bridged stream: the upstream speaks Responses SSE and the
				// client asked for chat.completion.chunk. Usage is read off the
				// raw upstream events inside the translator, so billing matches
				// the native path byte for byte. `model` is the client-facing
				// name — the rendered frames must echo what was requested, not
				// the relay's model_map'd name, which is why rewriteClientModel
				// has no role here.
				sawTerminal, rerr := streamCodexAsChatCompletions(c, br, &counts, model, chatStreamWantsUsage(body))
				clientGone = !sawTerminal && isClientDisconnect(ctx, rerr)
				if !sawTerminal && !clientGone {
					log.Warnf("codex proxy(apikey): bridged SSE ended before terminal event via %s: %v", a.ID, rerr)
				}
			} else {
				sse := streamSSEOpenAI(c, br, &counts, rewriteClientModel)
				clientGone = sse.clientGone
				// An in-band capacity/quota frame is why a turn can end with
				// output but no usage. Naming it separates "the relay shed this
				// turn" from "the relay is broken", which the bare
				// missing-usage warning below could never distinguish.
				if sse.shed {
					log.Warnf("codex proxy(apikey): %s shed the turn mid-stream (capacity=%v); the client sees a retryable error, not a session-ending one",
						a.ID, sse.capacity)
				}
				// A stream that stops without a terminal event is what the
				// client reports as "stream disconnected before completion".
				// This path had no way to see it until now.
				if !sse.sawTerminal && !clientGone {
					log.Warnf("codex proxy(apikey): %s SSE ended before terminal event (truncated upstream) — client will report a disconnect", a.ID)
					streamTruncated = true
				}
			}
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
		case bridged:
			// Non-streaming bridged request. The relay may still answer with an
			// SSE stream (several ignore `stream:false` on /v1/responses), so
			// dispatch on the bytes exactly like the streaming branch does and
			// aggregate when needed — the alternative is handing the whole-body
			// JSON parse an SSE payload and failing a good turn closed.
			//
			// Every failure below rolls back to the forward loop (retry=true)
			// rather than answering the client. Not one byte has been written
			// yet, so trying the next credential is invisible to the caller —
			// whereas a 502 here hands over a hard error while healthy
			// credentials sit unused. This is the same reasoning the retryable
			// status branch above applies before the response is committed.
			var payload []byte
			if responseIsSSE(resp.Header, br) {
				agg, aerr := aggregateCodexResponseStream(br, &counts)
				if aerr != nil {
					_ = resp.Body.Close()
					log.Warnf("codex proxy(apikey): bridged aggregation via %s failed: %v — rotating to next credential", a.ID, aerr)
					s.reportCodexAPIKeyFault(a, http.StatusBadGateway, time.Time{})
					s.emitLog(requestlog.Record{
						Client: clientName, ClientToken: maskClientToken(clientToken),
						Provider: auth.ProviderOpenAI, AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
						Model: model, Stream: stream, Path: path, Status: http.StatusBadGateway,
						DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: aerr.Error(),
					})
					return true, false
				}
				payload = agg
			} else {
				payload, _ = io.ReadAll(br)
				// Responses reports usage as input_tokens/output_tokens, not
				// the Chat Completions prompt_/completion_ pair, so this needs
				// the Codex extractor rather than extractOpenAIUsageFromJSON.
				counts.Add(extractCodexBackendUsageFromJSON(payload))
			}
			if usage.MissingUsage(counts) {
				_ = resp.Body.Close()
				log.Warnf("codex proxy(apikey): %s returned success without usage on bridged non-stream response — rotating to next credential", a.ID)
				s.reportCodexAPIKeyFault(a, http.StatusBadGateway, time.Time{})
				s.emitLog(requestlog.Record{
					Client: clientName, ClientToken: maskClientToken(clientToken),
					Provider: auth.ProviderOpenAI, AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
					Model: model, Stream: stream, Path: path, Status: http.StatusBadGateway,
					DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: usage.MissingUsageError,
				})
				return true, false
			}
			converted, cerr := apicompat.ResponsesToChatCompletion(payload, model, time.Now().Unix())
			if cerr != nil {
				_ = resp.Body.Close()
				log.Warnf("codex proxy(apikey): chat/completions render via %s failed: %v — rotating to next credential", a.ID, cerr)
				s.emitLog(requestlog.Record{
					Client: clientName, ClientToken: maskClientToken(clientToken),
					Provider: auth.ProviderOpenAI, AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
					Model: model, Stream: stream, Path: path, Status: http.StatusBadGateway,
					DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: cerr.Error(),
				})
				return true, false
			}
			// Content-Type is stamped after the allowlist copy: this branch may
			// have aggregated an SSE stream, so the upstream's
			// text/event-stream would misdescribe the single JSON object.
			downstream.CopyResponseHeaders(c.Writer.Header(), resp.Header, time.Now())
			c.Writer.Header().Set("Content-Type", "application/json")
			c.Writer.WriteHeader(http.StatusOK)
			_, _ = c.Writer.Write(converted)
		default:
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
				billed, err := s.saas.Charge(chargeCtx(c), info, auth.ProviderOpenAI, model, counts, costUSD)
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
	// Same label the OAuth path uses, so the two aggregate together in the
	// request log instead of hiding half the truncations under a clean 200.
	if errField == "" && streamTruncated {
		errField = "stream truncated before terminal event"
	}
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

// setJSONBool sets a top-level boolean field on a JSON object body, preserving
// every other field's raw bytes. Used to stamp the client's `stream` intent
// onto a bridged Responses body — apicompat's translator deliberately leaves
// the field unset, because the Codex backend path forces it in the sanitizer
// while a generic relay honors whatever the caller asks for.
func setJSONBool(body []byte, field string, v bool) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return body, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	obj[field] = b
	return json.Marshal(obj)
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

// sseRelayOutcome is what one API-key SSE relay observed, beyond the bytes it
// forwarded. Each field drives a different decision, so they are reported
// separately rather than collapsed into a status.
type sseRelayOutcome struct {
	// clientGone: the CLIENT is what ended the stream.
	clientGone bool
	// sawTerminal: a stream-terminating event arrived. Without one the client
	// raises "stream disconnected before completion", so its absence is a real
	// upstream fault and must be visible in the log rather than passed off as a
	// clean end-of-stream.
	sawTerminal bool
	// shed: upstream refused the turn mid-stream with a capacity/quota/rate
	// error frame inside an otherwise-200 response.
	shed bool
	// capacity: that shed was one of the two session-terminating capacity
	// codes, and was demoted on the way out.
	capacity bool
}

// streamSSEOpenAI is the OpenAI SSE passthrough. The wire format is `data:
// <json>\n\n` with a terminal `data: [DONE]`; Codex relays answering
// /v1/responses instead terminate with a `response.completed`-family event.
// Usage arrives in the final chunk when stream_options.include_usage is on (we
// always ensure that); parsing it here keeps billing correct for streaming
// clients.
//
// This path used to be a bare read/write loop while the OAuth path
// (streamSSECodexBackend) had three protections it lacked. Every one of them
// matters more here, because API-key relays now carry the bulk of Codex
// traffic:
//
//   - Keepalive. A model that thinks for a minute writes nothing, and the
//     intermediaries in between (Caddy, Cloudflare, the client's own idle
//     timeout) cut a silent connection. The client reports that as "stream
//     disconnected before completion".
//   - Terminal tracking. Without it a truncated upstream is indistinguishable
//     from a clean end, so the fault is invisible in the request log — which is
//     why these disconnects looked like they came from nowhere.
//   - Capacity demotion. Upstream sheds load as an in-band error frame inside a
//     200 stream. Forwarded verbatim, `server_is_overloaded` reaches the CLI as
//     ApiError::ServerOverloaded, which is TERMINAL for the session; the same
//     failure under nearly any other code lands in the CLI's Retryable arm and
//     is merely backed off. cc-core's codexerr.ClientFrame rewrites just that
//     code and leaves the human-readable message intact.
//
// Billing and health are read from the ORIGINAL payload, before demotion —
// after ClientFrame the code no longer says why the request failed.
//
// clientGone is load-bearing for billing: an upstream that never reported usage
// is a relay fault (bill nothing, cool the credential), while a client hang-up
// is the user's own choice (bill the partial usage, leave health alone).
// Conflating the two is what made every Ctrl-C both overcharge and trip the
// breaker. The read error alone cannot tell them apart — an upstream RST and a
// client disconnect surface identically at the reader — so the signal comes
// from the request context, exactly as the Anthropic and Codex-OAuth paths do.
func streamSSEOpenAI(c *gin.Context, reader *bufio.Reader, counts *usage.Counts, rewriteClientModel string) sseRelayOutcome {
	flusher, _ := c.Writer.(http.Flusher)
	// c.Request is nil in unit tests that drive the relay directly; treat that
	// as "no cancellation signal" rather than panicking on the hot path.
	var ctx context.Context
	if c.Request != nil {
		ctx = c.Request.Context()
	}

	var out sseRelayOutcome
	next := func() (emit []byte, terminal bool, err error) {
		line, rerr := reader.ReadBytes('\n')
		if len(line) == 0 {
			return nil, false, rerr
		}
		trim := bytes.TrimRight(line, "\r\n")
		outLine := line
		if bytes.HasPrefix(trim, []byte("data:")) {
			payload := bytes.TrimSpace(trim[5:])
			switch {
			case bytes.Equal(payload, []byte("[DONE]")):
				terminal = true
			case len(payload) > 0 && payload[0] == '{':
				// Accounting first, on the untouched payload.
				counts.Add(extractOpenAIUsageFromJSON(payload))
				if codexTerminalEvent(payload) {
					terminal = true
				}
				if codexerr.Classify(payload) == codexerr.ClassRetryable {
					out.shed = true
				}
				rewritten := payload
				if rewriteClientModel != "" {
					if r := rewriteResponseModel(rewritten, rewriteClientModel); r != nil {
						rewritten = r
					}
				}
				// Demote only the two session-ending capacity codes. Quota and
				// rate codes are left alone: the CLI handles them
				// non-terminally and parses its retry delay off the original.
				if frame, shed, capacity := codexerr.ClientFrame(rewritten); shed {
					rewritten = frame
					if capacity {
						out.capacity = true
					}
				}
				if !bytes.Equal(rewritten, payload) {
					tail := line[len(trim):]
					rebuilt := make([]byte, 0, len("data: ")+len(rewritten)+len(tail))
					rebuilt = append(rebuilt, []byte("data: ")...)
					rebuilt = append(rebuilt, rewritten...)
					rebuilt = append(rebuilt, tail...)
					outLine = rebuilt
				}
			}
		}
		return outLine, terminal, rerr
	}

	// commit=nil: this path commits headers eagerly before the relay starts
	// (writeSSEResponseHeaders), matching the OAuth passthrough.
	r := ccstream.Relay(c.Writer, func() {
		if flusher != nil {
			flusher.Flush()
		}
	}, ccstream.RelayOptions{
		KeepaliveIdle:    10 * time.Second,
		KeepalivePayload: []byte(":\n\n"),
		Next:             next,
	})
	out.sawTerminal = r.SawTerminal
	out.clientGone = isClientDisconnect(ctx, r.Err)
	return out
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
