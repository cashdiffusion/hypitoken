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

	"github.com/wjsoj/cc-core/apicompat"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/codexerr"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
	ccstream "github.com/wjsoj/cc-core/stream"
	"github.com/wjsoj/cc-core/usage"
)

// The Codex CLI fingerprint (codex-tui/0.135.0 identity over the legacy HTTP
// POST /codex/responses path) is now centralized in cc-core's mimicry package
// (mimicry.ApplyCodexCLIHeaders — no OpenAI-Beta on this path; real Codex sets
// it only on the WS handshake), shared with CPA-Claude so every relay stays
// in lockstep when the version target is bumped. See cc-core/mimicry/codex.go
// and cc-core/crack/codex/SPEC.md.

// The Codex backend request-shaping logic (path mapping + body sanitization)
// now lives in cc-core's mimicry package (mimicry.CodexOAuthPath /
// mimicry.SanitizeCodexRequestBody / mimicry.StripThinkingSuffix), shared with
// CPA-Claude. See cc-core/mimicry/codex_body.go.

// doForwardCodexOAuth forwards the client's /v1/responses request to the
// ChatGPT backend. Behavior matches the vendor Codex CLI: Bearer auth from
// the OAuth access_token, account_id from the cached ID-token claims, a
// fresh per-request session UUID, and the `codex-tui` User-Agent /
// Originator that the backend fingerprints on.
func (s *Server) doForwardCodexOAuth(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, clientName string, start time.Time, attempts int) (retry, done bool) {
	// /v1/chat/completions is bridged onto the backend's /codex/responses route
	// (codex_chat_bridge.go): the request is translated into a Responses body on
	// the way up and the Responses stream/object is rendered back as
	// chat.completion{,.chunk} on the way down. Without the bridge every
	// OpenAI-compatible client was unroutable to an OAuth subscription
	// credential and could only be served by a paid relay key.
	isChat := path == "/v1/chat/completions"
	if !isChat && path != "/v1/responses" && path != "/v1/responses/compact" {
		// Any other route genuinely has no backend equivalent. Ask the retry
		// loop to try a different credential; don't MarkFailure — this
		// credential isn't broken, just the wrong kind.
		log.Debugf("codex oauth: %s skipping %s (no ChatGPT backend equivalent)", a.ID, path)
		return true, false
	}

	snap := a.Snapshot()
	baseURL := strings.TrimRight(s.cfg.ChatGPTBackendBaseURL, "/") + "/codex"
	// Per-credential base URL override is allowed for vendor-relay setups.
	if ab := strings.TrimRight(snap.BaseURL, "/"); ab != "" {
		baseURL = ab
	}
	// A bridged chat request is a Responses request from here on: same backend
	// route, same sanitizer, same fingerprint.
	upstreamPath := path
	if isChat {
		upstreamPath = "/v1/responses"
	}
	upURL := baseURL + mimicry.CodexOAuthPath(upstreamPath)

	sourceBody := body
	if isChat {
		converted, cerr := apicompat.ChatCompletionsToResponses(body)
		if cerr != nil {
			// A body we can't translate is a client-shape problem, not a
			// credential problem — but an API-key credential forwards
			// chat/completions verbatim and may well accept it, so roll back to
			// the loop instead of failing the request here.
			log.Infof("codex oauth: chat/completions bridge declined body via %s: %v — deferring to API-key path", a.ID, cerr)
			return true, false
		}
		sourceBody = converted
	}

	upstreamBody, _, err := mimicry.SanitizeCodexRequestBody(sourceBody, upstreamPath)
	if err != nil {
		log.Warnf("codex oauth: body sanitize failed via %s: %v", a.ID, err)
		upstreamBody = sourceBody
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
	accountID, _ := a.CodexIdentity()
	isCompactPath := path == "/v1/responses/compact"
	// Apply the Codex CLI fingerprint — codex-tui identity (Originator /
	// User-Agent / Version) over the HTTP POST /codex/responses{,/compact}
	// path. Centralized in cc-core (mimicry.ApplyCodexCLIHeaders) so every relay
	// stays in lockstep when the version target is bumped. See cc-core/crack/codex/SPEC.md.
	//
	// The routing hint is derived from upstreamBody — the bytes actually going
	// out, after sanitization — so the header can never name a different model
	// than the body does.
	routingModel, routingTier := mimicry.CodexModelAndTier(upstreamBody)
	mimicry.ApplyCodexCLIHeaders(upReq, accessToken, accountID, isCompactPath, routingModel, routingTier)

	// Shared pooled transport (per proxyURL). Reusing HTTP/2 connections is
	// critical here: chatgpt.com's CF edge rate-limits new TCP/TLS connections
	// from VPS/proxy IPs and RSTs the handshake when the per-IP new-connection
	// quota is hit — the classic alternating 200/503 symptom. A pooled h2 conn
	// carries many requests so we stay under the limit. ClientFor's transport
	// has HTTP/2 PING health checks (utls.go) so stale reused conns are
	// detected and re-dialed transparently.
	client := auth.ClientFor(snap.ProxyURL, s.cfg.UseUTLS)
	// Transient wire-level flaps (CF edge RST mid-handshake, h2 PROTOCOL_ERROR /
	// REFUSED_STREAM, `connection reset by peer`, stale pooled h2 conn) are
	// replayed with exponential backoff + jitter inside ClientFor's transport
	// (cc-core auth.retryRoundTripper) on this same credential — see
	// auth.IsTransientNetErr. By the time Do returns an error, that backoff loop
	// is already exhausted, so a transient error surviving to here means the
	// flap is persistent; we defer to the outer loop (another credential)
	// without MarkFailure rather than burning this one.
	resp, err := client.Do(upReq)
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
		// Bridged chat clients routinely ask for relay-only model names
		// (gpt-5.6-terra-high, gpt-4o-mini, …) that the ChatGPT backend doesn't
		// host. Before the bridge those requests went straight to an API key;
		// now that OAuth is tried first, a model rejection must roll back to
		// the API-key pool rather than surface a 400 the client can't act on.
		// Scoped to the bridged route so native /v1/responses keeps relaying
		// its upstream 4xx verbatim. The credential is healthy — no MarkFailure.
		if isChat && codexModelUnsupported(resp.StatusCode, errBody) {
			log.Infof("codex oauth: %s does not host model %s (upstream %d) — rotating to an API-key credential", a.ID, model, resp.StatusCode)
			return true, false
		}
		copySafeRetryHeaders(c, resp.Header)
		writeAPIError(c, auth.ProviderOpenAI, publicUpstreamError(resp.StatusCode, errBody))
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
	var streamErr string
	// Status recorded in the request log. Defaults to the upstream's, but a
	// mid-stream client hang-up overrides it to 499 — the response was 200 on
	// the wire, yet logging it as a success with an error attached hides it
	// from every "client canceled" view.
	logStatus := resp.StatusCode
	switch {
	case isCompactPath:
		// /codex/responses/compact returns a single JSON object — no SSE.
		// Read it once, extract usage, pass through verbatim. Matches sub2api's
		// handleNonStreamingResponsePassthrough behavior on this path.
		payload, rerr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if rerr != nil {
			log.Warnf("codex oauth: read compact body via %s: %v", a.ID, rerr)
			writeAPIError(c, auth.ProviderOpenAI, APIError{Status: http.StatusBadGateway, Code: "service_response_error", Message: "The model service returned an unreadable response. Please try again."})
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
		// Streaming client: passthrough SSE verbatim (with keepalive + terminal
		// tracking), or — on the bridged chat route — translated frame by frame
		// into chat.completion.chunk. A truncated upstream (no terminal event)
		// is surfaced in the request log instead of looking like a clean stream
		// end, on both paths.
		writeResponseHeaders(c, resp)
		relay := func() (bool, error) { return streamSSECodexBackend(c, resp, &counts) }
		if isChat {
			wantsUsage := chatStreamWantsUsage(body)
			relay = func() (bool, error) {
				return streamCodexAsChatCompletions(c, resp, &counts, model, wantsUsage)
			}
		}
		if sawTerminal, rerr := relay(); !sawTerminal {
			// A stream that ends without a terminal event is only an upstream
			// fault when the upstream is what went away. The far more common
			// cause is the client hanging up mid-turn — Codex CLI aborts the
			// request on Ctrl-C / ESC — which cancels c.Request.Context() and
			// surfaces here as a read error, indistinguishable from truncation
			// unless the context is consulted.
			//
			// Conflating the two made ordinary user behaviour look like an
			// upstream incident: in production this label accounted for ~90% of
			// all recorded Codex errors, drowning out the ~0.05% of genuine h2
			// truncations. Match the transport-error branch above (and the
			// Anthropic path) and name each for what it is.
			if isClientDisconnect(ctx, rerr) {
				streamErr = "client canceled"
				// 499 + MarkClientCancel match the pre-stream disconnect branch
				// above, so a mid-stream hang-up lands in the same bucket as one
				// that happened a second earlier instead of as a 200 carrying an
				// error string. MarkClientCancel is health-neutral by design —
				// the credential did nothing wrong.
				logStatus = 499
				a.MarkClientCancel("client canceled mid-stream")
				log.Infof("codex oauth: client canceled mid-stream via %s", a.ID)
			} else {
				streamErr = "stream truncated before terminal event"
				log.Warnf("codex oauth: SSE stream ended before terminal event (truncated upstream) via %s: %v", a.ID, rerr)
			}
		}
	default:
		// Non-streaming client: aggregate SSE into a single response object
		// (mirrors CLIProxyAPI's CodexExecutor.Execute aggregation).
		payload, aerr := aggregateCodexResponseStream(resp.Body, &counts)
		if aerr != nil {
			log.Warnf("codex oauth: aggregation via %s failed: %v", a.ID, aerr)
			writeAPIError(c, auth.ProviderOpenAI, APIError{Status: http.StatusBadGateway, Code: "service_response_error", Message: "The model service returned an incomplete response. Please try again."})
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
		if isChat {
			converted, cerr := apicompat.ResponsesToChatCompletion(payload, model, time.Now().Unix())
			if cerr != nil {
				log.Warnf("codex oauth: chat/completions render via %s failed: %v", a.ID, cerr)
				writeAPIError(c, auth.ProviderOpenAI, APIError{Status: http.StatusBadGateway, Code: "service_response_error", Message: "The model service returned an incomplete response. Please try again."})
				_ = resp.Body.Close()
				s.emitLog(requestlog.Record{
					Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.ProviderOpenAI,
					AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model,
					Stream: stream, Path: path, Status: 502, Attempts: attempts,
					DurationMs: time.Since(start).Milliseconds(),
					Error:      cerr.Error(),
				})
				return false, true
			}
			payload = converted
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
	// CostUSD = official upstream price, BilledUSD = wallet debit.
	var costUSD, billedUSD float64
	var userID int64
	var multiplier float64
	if resp.StatusCode < 400 && counts.Requests > 0 && clientToken != "" {
		official := s.pricing.Cost(auth.ProviderOpenAI, model, counts)
		costUSD = official
		billedUSD = official
		// Same single-funnel as Anthropic: hand the official cost to the
		// SaaS adapter, get back the billed amount, log/charge with it.
		if info, ok := saasInfoFrom(c); ok && s.saas != nil {
			billed, err := s.saas.Charge(c.Request.Context(), info, auth.ProviderOpenAI, model, counts, official)
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
		BilledUSD:   billedUSD,
		UserID:      userID,
		Multiplier:  multiplier,
		Status:      logStatus,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      stream,
		Path:        path,
		Attempts:    attempts,
		Error:       streamErr,
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

// codexTerminalEvent reports whether a Codex backend SSE data payload is a
// stream-terminating event. The client (codex-core) waits for one of these; if
// the upstream stream EOFs without it, the client raises
// "stream disconnected before completion".
func codexTerminalEvent(payload []byte) bool {
	var ev struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(payload, &ev) != nil {
		return false
	}
	switch ev.Type {
	case "response.completed", "response.failed", "response.incomplete",
		"response.cancelled", "response.canceled":
		return true
	}
	return false
}

// streamSSECodexBackend is the Codex backend SSE passthrough. The format
// differs from OpenAI's API-key response: events carry JSON payloads
// structured as `response.completed` / `response.output_item.done` etc.
// Usage arrives inside the `response.completed` event as
// `response.usage.{input_tokens, output_tokens, input_tokens_details.cached_tokens}`.
//
// Beyond verbatim passthrough it (a) emits an SSE keepalive comment line during
// silent gaps so intermediaries (Caddy/Cloudflare/the client's own idle timeout)
// don't cut the long-lived stream while the model is mid-think, and (b) tracks
// whether the terminal event arrived, so a truncated upstream is logged instead
// of being passed off to the client as a clean end-of-stream (the root cause of
// the "stream disconnected before completion" reports). Returns whether a
// terminal event was observed.
//
// gin's ResponseWriter is not goroutine-safe, so the keepalive goroutine and the
// read loop share one mutex around every Write/Flush.
func streamSSECodexBackend(c *gin.Context, resp *http.Response, counts *usage.Counts) (sawTerminal bool, streamErr error) {
	flusher, _ := c.Writer.(http.Flusher)
	reader := newLineReader(resp.Body)

	// next supplies framing (raw lines) + usage + terminal detection; the shared
	// cc-core/stream.Relay owns keepalive + write serialization. commit=nil — the
	// caller commits headers eagerly on this path (verbatim passthrough).
	next := func() (out []byte, terminal bool, err error) {
		line, rerr := reader.readLine()
		if len(line) > 0 {
			trim := bytes.TrimRight(line, "\r\n")
			if bytes.HasPrefix(trim, []byte("data:")) {
				payload := bytes.TrimSpace(trim[5:])
				if len(payload) > 0 && payload[0] == '{' {
					counts.Add(extractCodexBackendUsageFromJSON(payload))

					// Upstream sheds capacity as an in-band error frame inside
					// an otherwise-200 stream. Forwarded verbatim it reaches the
					// CLI as ApiError::ServerOverloaded, which is TERMINAL for
					// the session ("Selected model is at capacity") — while the
					// same failure under nearly any other code lands in the
					// CLI's Retryable arm and is simply backed off. Demote just
					// those two codes; the message is left untouched, so the
					// user still sees why. See cc-core/codexerr.
					//
					// Unlike CPA-Claude this path cannot instead WITHHOLD the
					// frame and fail over: the caller commits response headers
					// eagerly above (writeResponseHeaders before the relay), so
					// by the time any frame is classified the response is
					// already committed. Demotion is the whole fix available
					// here; giving this path lazy commit would be a separate
					// change.
					if codexerr.Classify(payload) == codexerr.ClassRetryable {
						if demoted, ok := codexerr.DemoteCapacityCode(payload); ok {
							tail := line[len(trim):]
							rebuilt := make([]byte, 0, len("data: ")+len(demoted)+len(tail))
							rebuilt = append(rebuilt, []byte("data: ")...)
							rebuilt = append(rebuilt, demoted...)
							rebuilt = append(rebuilt, tail...)
							line = rebuilt
						}
					}

					if codexTerminalEvent(payload) {
						terminal = true
					}
				}
			}
		}
		return line, terminal, rerr
	}

	r := ccstream.Relay(c.Writer, func() {
		if flusher != nil {
			flusher.Flush()
		}
	}, ccstream.RelayOptions{
		KeepaliveIdle:    10 * time.Second,
		KeepalivePayload: []byte(":\n\n"),
		Next:             next,
	})
	return r.SawTerminal, r.Err
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
