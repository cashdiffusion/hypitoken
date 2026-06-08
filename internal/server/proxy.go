package server

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/thinkingsig"
	"github.com/wjsoj/cc-core/usage"
)

// hopHeaders are stripped when forwarding to upstream.
var hopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
	// Anthropic auth is set by us — strip anything the client sent.
	"Authorization": true,
	"X-Api-Key":     true,
	"X-Client-Ip":   true,
}

func (s *Server) handleMessages(c *gin.Context) {
	s.forward(c, auth.ProviderAnthropic, "/v1/messages")
}

func (s *Server) handleCountTokens(c *gin.Context) {
	s.forward(c, auth.ProviderAnthropic, "/v1/messages/count_tokens")
}

// forward runs the per-provider retry loop and credential routing for a
// single client request. `provider` picks the credential pool subset; `path`
// is the provider-native upstream path. doForward still assumes Anthropic
// semantics for request shaping — Codex has its own doForward variant (see
// codex_proxy.go) which this dispatcher will call once provider != anthropic.
func (s *Server) forward(c *gin.Context, provider, path string) {
	clientTok, _ := c.Get("client_token")
	clientToken, _ := clientTok.(string)
	if clientToken == "" {
		clientToken = c.ClientIP()
	}
	clientNameV, _ := c.Get("client_name")
	clientName, _ := clientNameV.(string)
	start := time.Now()

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "read body: " + err.Error()})
		return
	}

	// Per-window slot identity. Each Claude Code CLI window sends a distinct
	// X-Claude-Code-Session-Id, so the same user opening multiple windows is
	// scheduled as multiple independent slots (and can land on different
	// upstream credentials). Empty for raw API callers → one slot per token.
	slotID := clientSlotID(c)

	// Parse minimal request metadata for usage reporting + streaming detection.
	var peek struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &peek)
	model := peek.Model
	if model == "" {
		model = "unknown"
	}

	// SaaS path: balance + per-token caps pre-check; credential group from user's
	// pricing group. Legacy weekly-budget logic still applies to non-SaaS tokens.
	saasInfo, saasOK := saasInfoFrom(c)
	var clientGroup string
	// clientGroups is the priority-ordered fallthrough list for multi-channel
	// routing (anthropic vs kiro). Populated from token.EffectiveGroups() in
	// the non-SaaS branch, and from saasInfo.Groups (when non-empty) in the
	// SaaS branch. Empty = single-channel anthropic, like before.
	var clientGroups []string
	if saasOK && len(saasInfo.Groups) > 0 {
		clientGroups = append(clientGroups, saasInfo.Groups...)
	}
	if saasOK && s.saas != nil {
		clientGroup = s.saas.CredentialGroup(saasInfo)
		if pre := s.saas.PreCheck(c.Request.Context(), saasInfo); pre != nil {
			c.AbortWithStatusJSON(pre.Status, pre.Body)
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider, Model: model,
				Stream: peek.Stream, Path: path, Status: pre.Status,
				DurationMs: time.Since(start).Milliseconds(), Error: "saas pre-check rejected",
			})
			return
		}
	} else {
		entry, tokOK := s.tokens.Lookup(clientToken)
		var weeklyLimit float64
		if tokOK {
			weeklyLimit = entry.WeeklyUSD
			clientGroup = entry.Group
			// Capture the priority-ordered Groups for kiro routing below.
			// Falls back to [Group] (or [""]) when only the legacy field is set.
			clientGroups = entry.EffectiveGroups()
		}
		if tokOK && weeklyLimit > 0 {
			spent := s.usage.WeeklyCostUSD(clientToken)
			if spent >= weeklyLimit {
				c.Header("Retry-After", "604800")
				c.AbortWithStatusJSON(429, gin.H{
					"error":     "weekly budget exceeded",
					"spent_usd": spent,
					"limit_usd": weeklyLimit,
					"week":      s.usage.CurrentWeekKey(),
				})
				s.emitLog(requestlog.Record{
					Client:      clientName,
					ClientToken: maskClientToken(clientToken),
					Provider:    provider,
					Model:       model,
					Stream:      peek.Stream,
					Path:        path,
					Status:      429,
					DurationMs:  time.Since(start).Milliseconds(),
					Error:       "weekly budget exceeded",
				})
				return
			}
		}
	}

	// Fail fast when the route can't be served by any available credential.
	// OAuth Codex credentials only speak /v1/responses — they can't serve
	// /v1/chat/completions, and without this check the forward loop would
	// cycle every OAuth cred (each returning retry=true), then surface a
	// misleading 503 "all upstream credentials exhausted". If no API-key
	// credential of this provider can serve the requested model, tell the
	// client directly what's wrong.
	if auth.NormalizeProvider(provider) == auth.ProviderOpenAI && path == "/v1/chat/completions" && !s.pool.HasAPIKeyFor(provider, clientGroup, model) {
		msg := fmt.Sprintf("model %q is only available via /v1/responses on this server (no OpenAI-compatible API-key credential is configured for it); retry with the /v1/responses endpoint", model)
		c.AbortWithStatusJSON(400, gin.H{"error": msg})
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider, Model: model,
			Stream: peek.Stream, Path: path, Status: 400,
			DurationMs: time.Since(start).Milliseconds(), Error: "route unsupported for available credentials",
		})
		return
	}

	// Rate limit (RPM) per client token. Sliding 60s window; scoped
	// per-provider to match the inflight budget so Claude and Codex don't
	// share one cap. Checked before the concurrency gate so a burst of
	// 429s doesn't briefly occupy slots.
	rpmKey := auth.NormalizeProvider(provider) + "|" + clientToken
	if limit := s.clientRPM(c, clientToken); limit > 0 {
		if ok, retry := s.rpm.Allow(rpmKey, limit); !ok {
			c.Header("Retry-After", strconv.Itoa(retry))
			c.AbortWithStatusJSON(429, gin.H{
				"error":       "rate limit exceeded",
				"rpm_limit":   limit,
				"retry_after": retry,
			})
			s.emitLog(requestlog.Record{
				Client:      clientName,
				ClientToken: maskClientToken(clientToken),
				Provider:    provider,
				Model:       model,
				Stream:      peek.Stream,
				Path:        path,
				Status:      429,
				DurationMs:  time.Since(start).Milliseconds(),
				Error:       "rpm limit exceeded",
			})
			return
		}
	}

	// Concurrency limit per client token.
	maxConc := s.clientMaxConcurrent(c, clientToken)
	if maxConc > 0 {
		// Scope the counter per provider so Claude and Codex share a token
		// but not a concurrency bucket — matches the per-provider session
		// keying in Pool.Acquire.
		inflightKey := auth.NormalizeProvider(provider) + "|" + clientToken
		cur, releaseSlot := s.inflight.Begin(inflightKey)
		defer releaseSlot()
		if int(cur) > maxConc {
			c.Header("Retry-After", "5")
			c.AbortWithStatusJSON(429, gin.H{
				"error":          "too many concurrent requests",
				"max_concurrent": maxConc,
				"in_flight":      int(cur),
			})
			s.emitLog(requestlog.Record{
				Client:      clientName,
				ClientToken: maskClientToken(clientToken),
				Provider:    provider,
				Model:       model,
				Stream:      peek.Stream,
				Path:        path,
				Status:      429,
				DurationMs:  time.Since(start).Milliseconds(),
				Error:       "concurrent limit exceeded",
			})
			return
		}
	}

	// Kiro side-channel: if the resolved token has a kiro-routed group in its
	// priority list, AND that group has healthy Kiro credentials, dispatch via
	// kirobridge and return. Falls through to the anthropic path below when no
	// kiro group matches OR no kiro creds are healthy.
	if path == "/v1/messages" && s.tryKiro(c, body, model, peek.Stream, clientToken, clientName, path, clientGroups, start) {
		return
	}

	// Hand off to the credential-failover retry loop.
	s.forwardWithFailover(c, provider, path, model, clientToken, clientGroup, clientName, slotID, body, peek.Stream, start)
}

// forwardWithFailover runs the per-request retry loop: it acquires a
// credential, forwards via the provider-appropriate doForward, and on a
// credential-level error (429 quota/rate-limit, 401/403, account ban — which
// doForward withholds rather than writing through) transparently switches to
// another healthy credential. The user only ever sees an error when the pool
// has no slot left: excludeIDs narrows the candidate set each round so the
// loop terminates naturally once Acquire returns nil (every healthy credential
// tried). maxAttempts is only a backstop against a pathologically large
// all-failing fleet. When every credential is exhausted, the most recent
// withheld upstream error is replayed verbatim (e.g. a 429 + Retry-After)
// instead of a synthetic 503, so clients back off correctly.
func (s *Server) forwardWithFailover(c *gin.Context, provider, path, model, clientToken, clientGroup, clientName, slotID string, body []byte, stream bool, start time.Time) {
	const maxAttempts = 12
	tried := make(map[string]bool)
	attempts := 0
	var lastDeferred *deferredResponse

	// surfaceDeferred replays a withheld upstream error to the client once no
	// healthy credential remains — preferable to a synthetic 503 because the
	// client (e.g. Claude Code) backs off correctly on the genuine 429 +
	// Retry-After it would otherwise have received directly.
	surfaceDeferred := func(d *deferredResponse) {
		replayDeferred(c, d)
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider,
			AuthID: d.authID, Model: model, Status: d.status, Attempts: attempts,
			Stream: stream, Path: path, DurationMs: time.Since(start).Milliseconds(),
			Error: fmt.Sprintf("upstream %d (all credentials exhausted)", d.status),
		})
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		excludeIDs := make([]string, 0, len(tried))
		for id := range tried {
			excludeIDs = append(excludeIDs, id)
		}
		a := s.pool.Acquire(c.Request.Context(), provider, clientToken, clientGroup, model, slotID, excludeIDs...)
		if a == nil {
			// No healthy/untried credential left. If we withheld an upstream
			// error on the way here, surface that genuine status; otherwise
			// there was nothing in the pool to serve the request at all.
			if lastDeferred != nil {
				surfaceDeferred(lastDeferred)
				return
			}
			msg := "no upstream credentials available"
			if len(tried) > 0 {
				msg = "all upstream credentials exhausted"
			}
			c.AbortWithStatusJSON(503, gin.H{"error": msg})
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider, Model: model,
				Stream: stream, Path: path, Status: 503, Attempts: attempts,
				DurationMs: time.Since(start).Milliseconds(), Error: msg,
			})
			return
		}
		tried[a.ID] = true
		attempts++

		var retry, done bool
		var deferred *deferredResponse
		switch auth.NormalizeProvider(a.Provider) {
		case auth.ProviderOpenAI:
			retry, done = s.doForwardCodex(c, a, path, body, stream, model, clientToken, clientName, start, attempts)
		default:
			// attempt > 0 ⇒ this is a transparent retry; doForward skips the
			// blocking bootstrap-wait so the credential switch stays fast.
			retry, done, deferred = s.doForward(c, a, path, body, stream, model, clientToken, slotID, clientName, start, attempts, attempt > 0)
		}
		if done {
			s.pool.Release(provider, clientToken, slotID)
			return
		}
		if !retry {
			s.pool.Release(provider, clientToken, slotID)
			return
		}
		// Credential-level error withheld from the client — remember the most
		// recent one so it can be surfaced if every credential is exhausted,
		// then loop on to the next credential.
		if deferred != nil {
			lastDeferred = deferred
		}
		log.Warnf("proxy: retrying with a different credential (last auth=%s)", a.ID)
	}
	// Backstop reached (maxAttempts) — surface the last withheld error if any.
	if lastDeferred != nil {
		surfaceDeferred(lastDeferred)
		return
	}
	c.AbortWithStatusJSON(503, gin.H{"error": "upstream retries exhausted"})
	s.emitLog(requestlog.Record{
		Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider, Model: model,
		Stream: stream, Path: path, Status: 503, Attempts: attempts,
		DurationMs: time.Since(start).Milliseconds(), Error: "upstream retries exhausted",
	})
}

func (s *Server) emitLog(r requestlog.Record) {
	if s.reqLog == nil {
		return
	}
	s.reqLog.Log(r)
}

// clientSlotID derives a per-window slot identifier from the incoming request.
// Claude Code sends a stable per-window X-Claude-Code-Session-Id header (also
// mirrored in metadata.user_id.session_id); the Codex CLI sends a session_id
// header. Treating each distinct value as its own pool slot lets one user's
// multiple CLI windows occupy independent slots and be load-balanced across
// different credentials. Returns "" when the client supplies neither (raw API
// callers) — the pool then keeps one slot per client token.
func clientSlotID(c *gin.Context) string {
	if v := strings.TrimSpace(c.GetHeader("X-Claude-Code-Session-Id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.GetHeader("Session_id")); v != "" {
		return v
	}
	return ""
}

func maskClientToken(t string) string {
	if len(t) <= 10 {
		return "***"
	}
	return t[:6] + "…" + t[len(t)-4:]
}

// flagStripThinking persists the strip-thinking decision on a credential after a
// thinking-signature recovery succeeds, so future requests on it sanitize prior
// thinking signatures proactively (ahead of the forward) instead of failing once
// per request and replaying. Generic across all credentials/providers (any relay
// that rotates backend accounts and rejects echoed signatures gets flagged on its
// first signature recovery). Idempotent + best-effort.
func flagStripThinking(a *auth.Auth) {
	if a.StripThinkingEnabled() {
		return
	}
	if err := a.MarkStripThinking(); err != nil {
		log.Warnf("proxy: %s strip-thinking persist failed: %v", a.ID, err)
		return
	}
	log.Infof("proxy: %s flagged strip-thinking (persisted) — prior thinking signatures will be sanitized proactively on future requests", a.ID)
}

// deferredResponse is an upstream error response withheld from the client so
// the request can be transparently retried on another credential. The forward
// loop keeps the most recent one; if every healthy credential is exhausted it
// replays this verbatim instead of synthesizing a 503, so the client still
// receives the genuine upstream status (e.g. a 429 with its Retry-After) and
// backs off correctly. nil when nothing was withheld.
type deferredResponse struct {
	status int
	header http.Header
	body   []byte
	authID string
}

// replayDeferred writes a withheld upstream error response to the client,
// honouring the same hop-by-hop header filter as writeResponseHeaders.
func replayDeferred(c *gin.Context, d *deferredResponse) {
	for k, vs := range d.header {
		if hopHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.WriteHeader(d.status)
	_, _ = c.Writer.Write(d.body)
}

// doForward sends the request with one credential. Returns (retry, done, deferred):
//
//	retry=true   → caller should try another credential. When the retry was
//	               prompted by a credential-level upstream error (429 quota /
//	               rate-limit, 401/403, account ban) the response is withheld
//	               from the client and returned in deferred so the loop can
//	               surface it if no healthy credential remains. A nil deferred
//	               on retry=true means a transport error (nothing received).
//	done=true    → response was delivered to the client (status < 400 or a
//	               non-retryable error already written through).
//
// isRetry is true on the 2nd+ attempt of a request; it suppresses the
// blocking bootstrap-wait gate so a transparent credential switch doesn't
// re-stack the ≤5s sidecar wait on every alternate credential.
func (s *Server) doForward(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, slotID, clientName string, start time.Time, attempts int, isRetry bool) (retry bool, done bool, deferred *deferredResponse) {
	// Mid-conversation account switch: drop prior `thinking` block
	// signatures before forwarding. Both OAuth and API-key paths bind
	// thinking signatures to the issuing account, so this runs ahead
	// of the API-key branch. Scoped to /v1/messages — no other path
	// carries multi-turn assistant history.
	if path == "/v1/messages" {
		switched := s.switchTracker.Check(clientToken, body, a.ID)
		// StripThinkingEnabled credentials (relays that rotate backend accounts
		// per request) reject every echoed thinking signature, so sanitize ahead
		// of the forward instead of failing once and replaying. The flag is set +
		// persisted automatically on the first signature recovery (any provider).
		if switched || a.StripThinkingEnabled() {
			if switched {
				log.Infof("auth switch detected: clientToken=%s now on auth=%s — sanitizing prior thinking signatures",
					maskClientToken(clientToken), a.ID)
			}
			body = thinkingsig.SanitizeForSwitch(body)
		}
	}

	if a.Kind == auth.KindAPIKey {
		return s.doForwardAnthropicAPIKey(c, a, path, body, stream, model, clientToken, clientName, start, attempts)
	}
	baseURL := s.cfg.AnthropicBaseURL
	// Per-credential base URL override (used for relay/midstream vendors on
	// API-key credentials).
	if ab := strings.TrimRight(a.Snapshot().BaseURL, "/"); ab != "" {
		baseURL = ab
	}
	url := baseURL + path + "?beta=true"
	isAnthropicBase := strings.HasPrefix(strings.ToLower(baseURL), "https://api.anthropic.com")

	// Per-credential model rewrite (API-key only, e.g. third-party relays
	// that require a vendor-prefixed model name like "[0.1]a/claude-sonnet-4-6").
	// Routing already filtered to credentials that accept this model; here we
	// just substitute the body's "model" field if the map says so. The
	// client-facing model string used for usage/pricing stays unchanged.
	upstreamBody := body
	if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		if rewritten, err := rewriteModelField(body, upstreamModel); err == nil {
			upstreamBody = rewritten
		} else {
			log.Warnf("proxy: model rewrite (%s -> %s) failed via %s: %v", model, upstreamModel, a.ID, err)
		}
	}

	// Body-layer Claude Code mimicry: rebuild system to match the real CC
	// 4-block layout (billing block + "You are Claude Code..." + original
	// system prompt with cache_control), inject metadata.user_id with a
	// per-account device_id and a per-(account, clientToken, conversation)
	// session_id, sign the cch billing hash. The client's prompt is
	// preserved verbatim — only the surrounding wrapper is normalized.
	// Only runs on /v1/messages (count_tokens isn't billed and shouldn't
	// be modified). Haiku requests skip mimicry inside the function.
	id := SimIdentity{
		AccountKey:  accountAnchorFor(a),
		AccountUUID: a.AccountUUIDValue(),
		ClientToken: clientToken,
	}

	// Sidecar: dispatch the per-session bootstrap+quota_probe the first
	// time we see this (account, clientToken) pair. Real CC fires the
	// 9-step bootstrap (GrowthBook → settings → grove → bootstrap →
	// penguin → quota probe → mcp_servers → mcp_registry → releases)
	// BEFORE its first business /v1/messages — an OAuth bearer whose
	// very first observed traffic is /v1/messages with full system+tools
	// is a single-shot fingerprint of a non-CC client. Notify returns a
	// channel closed when bootstrap reaches the quota_probe step; we
	// gate the first business request on it, capped at bootstrapWaitCap
	// so a stuck sidecar can't hang user traffic.
	bootstrapReady := s.sidecar.Notify(a, clientToken)
	if path == "/v1/messages" {
		upstreamBody = applyClaudeCodeBodyMimicry(upstreamBody, model, id)
	}

	ctx := c.Request.Context()
	// Skip the blocking bootstrap-wait on retries: a transparent credential
	// switch (after a 429/auth error on the previous credential) must not
	// re-stack the ≤5s sidecar wait for every alternate credential. The
	// sidecar bootstrap still fires in the background via Notify above; we
	// just don't gate user traffic on it the second time around.
	if bootstrapReady != nil && !isRetry {
		select {
		case <-bootstrapReady:
		case <-ctx.Done():
			// client cancelled — let downstream layer handle it normally
		case <-time.After(bootstrapWaitCap):
			log.Warnf("sidecar: bootstrap-wait timeout for %s — proceeding without preceding bootstrap traffic", a.ID)
		}
	}
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(upstreamBody))
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": err.Error()})
		return false, true, nil
	}

	// Forward selected client headers.
	copyForwardableHeaders(c.Request.Header, upReq.Header)
	stripIngressHeaders(upReq.Header)

	// Anthropic auth + Claude Code fingerprint headers. Pass the same
	// SimIdentity so X-Claude-Code-Session-Id matches metadata.user_id.session_id.
	applyAnthropicHeaders(upReq, a, stream, isAnthropicBase, id, upstreamBody)

	client := auth.ClientFor(a.ProxyURL, s.cfg.UseUTLS)
	resp, err := client.Do(upReq)
	if err != nil {
		// Client went away (ctrl-C, closed connection, etc.) — not a
		// credential fault. Record a non-fatal hint for the admin panel,
		// skip retrying onto other credentials (they would all hit the
		// same dead context and get falsely blamed), and don't bother
		// writing a response body to the vanished client.
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			a.MarkClientCancel(err.Error())
			log.Infof("proxy: client canceled via %s: %v", a.ID, err)
			authKind := "oauth"
			if a.Kind == auth.KindAPIKey {
				authKind = "apikey"
			}
			s.emitLog(requestlog.Record{
				Client:      clientName,
				ClientToken: maskClientToken(clientToken),
				Provider:    auth.NormalizeProvider(a.Provider),
				AuthID:      a.ID,
				AuthLabel:   a.Label,
				AuthKind:    authKind,
				Model:       model,
				Stream:      stream,
				Path:        path,
				Status:      499, // nginx convention: client closed request
				DurationMs:  time.Since(start).Milliseconds(),
				Attempts:    attempts,
				Error:       "client canceled",
			})
			return false, true, nil
		}
		a.MarkFailure(err.Error())
		log.Warnf("proxy: upstream error via %s: %v", a.ID, err)
		return true, false, nil
	}

	// Decompress upstream gzip/br before reading anything — we asked for
	// gzip,br to match the real CC fingerprint, but every internal path
	// (usage parsing, SSE streamer, model rewrite, body forwarding) wants
	// plain bytes. The Content-Encoding header is also stripped so the
	// client receives identity even though upstream sent compressed.
	maybeDecompressResponse(resp)

	// Upstream error — log, do lightweight credential bookkeeping, and
	// faithfully forward the original response to the client as-is.
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		// Reactive signature-error recovery. A 400 "Invalid signature
		// in thinking block" means the past assistant turns echoed in
		// messages[] carry signatures bound to a different account
		// than this credential. Causes include: switch detector miss
		// (first-touch on a continuing conversation, server restart,
		// 2h GC eviction), or signatures generated outside this proxy.
		// One stateless rescue: drop the signed thinking blocks from
		// the request and replay on the same credential. If it still
		// fails, fall through to normal error handling.
		recovered := false
		if path == "/v1/messages" && thinkingsig.IsSignatureError(errBody) {
			sanitized := thinkingsig.SanitizeForSwitch(body)
			if !bytes.Equal(sanitized, body) {
				retryUpstream := sanitized
				if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
					if rewritten, err := rewriteModelField(retryUpstream, upstreamModel); err == nil {
						retryUpstream = rewritten
					}
				}
				retryUpstream = applyClaudeCodeBodyMimicry(retryUpstream, model, id)
				log.Warnf("proxy: %s returned 400 signature-in-thinking — sanitizing and retrying once on same credential", a.ID)
				if retryReq, rerr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(retryUpstream)); rerr == nil {
					copyForwardableHeaders(c.Request.Header, retryReq.Header)
					stripIngressHeaders(retryReq.Header)
					applyAnthropicHeaders(retryReq, a, stream, isAnthropicBase, id, retryUpstream)
					if retryResp, rderr := client.Do(retryReq); rderr == nil {
						maybeDecompressResponse(retryResp)
						if retryResp.StatusCode < 400 {
							log.Infof("proxy: %s signature retry succeeded", a.ID)
							resp = retryResp
							recovered = true
							flagStripThinking(a)
						} else {
							_ = retryResp.Body.Close()
							log.Warnf("proxy: %s signature retry still %d — surfacing original error", a.ID, retryResp.StatusCode)
						}
					} else {
						log.Warnf("proxy: %s signature retry transport error: %v", a.ID, rderr)
					}
				}
			}
		}
		if recovered {
			goto recoveredFromSignature
		}

		// Account-ban detection: Anthropic returns "organization has been
		// disabled" / "account has been disabled" on terminal bans, usually
		// with 401/403 but occasionally 400. These should hard-disable the
		// credential (manual clear required), not just cooldown.
		banned := isAccountBanBody(errBody)

		// Credential bookkeeping: mark the auth so the pool can make
		// smarter scheduling decisions, but never hide the error from
		// the client. Generic 4xx (400/404/413/422/...) are client-request
		// faults — credential is fine, so no MarkFailure.
		// retryable marks credential-level failures (this credential is bad
		// right now, but another might serve the request): 429 quota/rate-
		// limit, 401/403 auth rejection, account ban. For these we withhold
		// the response and let forward() transparently switch credentials so
		// the user never sees the error while the pool still has a slot.
		// Request-level faults (generic 4xx, "Extra usage is required") and
		// upstream-wide weather (5xx/529) stay non-retryable — every
		// credential would return the same thing, so forward it through.
		retryable := false
		switch {
		case banned:
			a.MarkHardFailure(fmt.Sprintf("account banned (upstream %d)", resp.StatusCode))
			log.Warnf("auth: %s hard-disabled — account ban detected (status %d)", a.ID, resp.StatusCode)
			retryable = true
		case resp.StatusCode == 429 && bytes.Contains(errBody, []byte("Extra usage is required")):
			// Request-level rejection (long context), not a credential
			// problem — no cooldown, no retry (every credential rejects it).
		case resp.StatusCode == 429:
			retryable = true
			// Four flavors of 429 from Anthropic, treated differently. Check
			// in this order — earlier checks are more specific signals:
			//
			//  1. Authoritative ratelimit headers — `anthropic-ratelimit-
			//     unified-status` (or `unified-5h-status` / `unified-7d-status`)
			//     == "rejected" together with `anthropic-ratelimit-unified-reset`
			//     (or per-bucket reset). This is the single most reliable quota
			//     signal Anthropic ships, present on every modern API call,
			//     regardless of body wording. Cool down until the stamped reset
			//     time so IsHealthy stays false until the credential genuinely
			//     recovers.
			//  2. Subscription usage limit ("Claude AI usage limit
			//     reached|<unix-ts>") — older / human-readable variant of (1).
			//     Honour the body timestamp.
			//  3. Stealth ban (no Retry-After, no anthropic-ratelimit-*
			//     headers, body is the generic rate_limit_error blurb):
			//     Anthropic occasionally serves bans this way. Hard-
			//     fail immediately so the credential stops cycling
			//     back into rotation every 30 seconds.
			//  4. Ordinary RPM/TPM rate limit: short cooldown +
			//     MarkRateLimited counter (15-strike escalation still
			//     applies as a backstop).
			//
			// Only (1) and (2) advance MarkUsageLimitReached (which deliberately
			// does NOT touch the consecutive-429 counter — those are real quota
			// signals, not stealth-ban candidates).
			if resetAt, banned, ok := parseUnifiedRatelimitRejected(resp.Header); ok && !banned {
				a.MarkUsageLimitReached(resetAt)
				log.Warnf("auth: %s usage limit (unified-ratelimit rejected) — cooldown until %s", a.ID, resetAt.Format(time.RFC3339))
			} else if resetAt, ok := parseClaudeUsageLimitBody(errBody); ok {
				a.MarkUsageLimitReached(resetAt)
				log.Warnf("auth: %s subscription usage limit — cooldown until %s", a.ID, resetAt.Format(time.RFC3339))
			} else {
				// "No reset signal" 429s — either unified-ratelimit
				// rejected with every reset stamp past/missing, or no
				// ratelimit headers at all. We don't know if the account
				// is banned or just genuinely rate-limited with a buggy
				// upstream payload, so defer the hard-fail decision to
				// the 15-strike accumulator inside MarkRateLimited
				// (rateLimit429HardFailureThreshold). One bad reply
				// shouldn't be enough to take a credential offline.
				resetAt := parseRetryAfter(resp.Header)
				s.pool.ReportUpstreamError(a, resp.StatusCode, resetAt)
			}
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			retryable = true
			// On 401, a body of {"type":"authentication_error", ...} or
			// "Invalid authentication credentials" means Anthropic has
			// definitively rejected this credential. For OAuth we just
			// refreshed the access token before this call, so a 401 here
			// is not a transient token-expiry — the underlying account
			// has been revoked or had its Opus/Sonnet entitlements
			// stripped. The default 60s cooldown just lets the credential
			// cycle back into rotation a minute later and 401 the next
			// random customer, so promote to sticky hard-failure instead.
			if resp.StatusCode == 401 && isDefinitiveAuthRejection(errBody) {
				a.MarkHardFailure(fmt.Sprintf("upstream 401 authentication rejected: %s", truncate(errBody, 200)))
				log.Warnf("auth: %s hard-disabled — definitive 401 authentication rejection", a.ID)
			} else {
				resetAt := parseRetryAfter(resp.Header)
				s.pool.ReportUpstreamError(a, resp.StatusCode, resetAt)
			}
		case resp.StatusCode == 529, resp.StatusCode >= 500:
			a.MarkFailure(fmt.Sprintf("upstream %d", resp.StatusCode))
		}

		authKind := "oauth"
		if a.Kind == auth.KindAPIKey {
			authKind = "apikey"
		}
		// Break sticky session so the retry (and any future request from this
		// client) can be assigned to a different, hopefully healthy credential.
		s.pool.Unstick(auth.NormalizeProvider(a.Provider), clientToken, slotID)

		if retryable {
			// Withhold the response and signal the caller to switch
			// credentials. We deliberately do NOT emitLog here: the request
			// hasn't reached the client yet, and logging a 429 row for an
			// attempt the user never saw would inflate the error dashboard and
			// mislead billing. The final outcome (a success on another
			// credential, or the surfaced error once the pool is exhausted)
			// logs the authoritative row. The journald warn line below keeps
			// per-credential visibility for ops.
			log.Warnf("proxy: %s returned %d — retrying on another credential. body=%s", a.ID, resp.StatusCode, truncate(errBody, 500))
			return true, false, &deferredResponse{
				status: resp.StatusCode,
				header: resp.Header.Clone(),
				body:   errBody,
				authID: a.ID,
			}
		}

		log.Warnf("proxy: %s returned %d — forwarding to client. body=%s", a.ID, resp.StatusCode, truncate(errBody, 500))
		s.emitLog(requestlog.Record{
			Client:      clientName,
			ClientToken: maskClientToken(clientToken),
			AuthID:      a.ID,
			AuthLabel:   a.Label,
			AuthKind:    authKind,
			Model:       model,
			Status:      resp.StatusCode,
			DurationMs:  time.Since(start).Milliseconds(),
			Stream:      stream,
			Path:        path,
			Attempts:    attempts,
			Error:       fmt.Sprintf("upstream %d", resp.StatusCode),
		})

		writeResponseHeaders(c, resp)
		_, _ = c.Writer.Write(errBody)
		return false, true, nil
	}

recoveredFromSignature:
	// Success or non-retryable error — stream response body to client.
	writeResponseHeaders(c, resp)

	var counts usage.Counts
	var sub subUsage
	counts.Requests = 1
	a.MarkSuccess()

	// When this credential rewrote the request's model name (relay vendors
	// with vendor-prefixed names), rewrite it back in the response so the
	// client keeps seeing the model it asked for. Claude Code uses the
	// model field on message_start to correlate conversation turns; a
	// vendor-prefixed name breaks multi-turn continuation.
	var rewriteClientModel string
	if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		rewriteClientModel = model
	}

	if stream && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		streamSSE(c, resp, &counts, &sub, rewriteClientModel)
	} else {
		respBody, _ := io.ReadAll(resp.Body)
		if rewriteClientModel != "" {
			respBody = rewriteResponseModel(respBody, rewriteClientModel)
		}
		_, _ = c.Writer.Write(respBody)
		counts.Add(extractUsageFromJSON(respBody, &sub))
	}
	_ = resp.Body.Close()
	authKind := "oauth"
	if a.Kind == auth.KindAPIKey {
		authKind = "apikey"
	}
	s.usage.Record(a.ID, a.Label, counts)
	// Compute the wallet-side cost: hand the official upstream price to the
	// SaaS adapter, which applies the per-(group × provider) multiplier and
	// returns the dollar amount actually deducted. The same value gets
	// written into the request log so the log row matches the wallet ledger.
	var costUSD float64
	var userID int64
	var multiplier float64
	if resp.StatusCode < 400 && counts.Requests > 0 && clientToken != "" {
		officialCost := s.pricing.Cost(auth.NormalizeProvider(a.Provider), model, counts)
		costUSD = officialCost
		if info, ok := saasInfoFrom(c); ok && s.saas != nil {
			billed, err := s.saas.Charge(c.Request.Context(), info, auth.NormalizeProvider(a.Provider), model, counts, officialCost)
			if err != nil {
				log.Warnf("saas: charge failed for token=%d user=%d: %v", info.TokenID, info.UserID, err)
			} else {
				costUSD = billed
				userID = info.UserID
				multiplier = s.saas.MultiplierFor(c.Request.Context(), info.GroupID, auth.NormalizeProvider(a.Provider))
			}
		}
	}
	// Advisor (server-side opus sub-call) is billed alongside the main
	// request: same auth absorbs the load, same client is charged, but the
	// requestlog gets a separate row per advisor model so by-model views
	// don't conflate sonnet-orchestrator cost with opus-advisor cost.
	advisorCost := s.recordSubUsage(c, a, authKind, clientToken, clientName, model, path, resp.StatusCode, sub)
	if resp.StatusCode < 400 && counts.Requests > 0 && clientToken != "" {
		// Single RecordClient call: weekly cost ledger should reflect the
		// total dollar cost of this /v1/messages call, advisor included.
		// Counts.Requests stays at 1 — advisor is a sub-call, not a request.
		var clientCounts usage.Counts
		clientCounts.Add(counts)
		for _, sc := range sub.byModel {
			clientCounts.Add(sc)
		}
		s.usage.RecordClient(clientToken, clientName, clientCounts, costUSD+advisorCost)
	}
	s.emitLog(requestlog.Record{
		Client:      clientName,
		ClientToken: maskClientToken(clientToken),
		Provider:    auth.NormalizeProvider(a.Provider),
		AuthID:      a.ID,
		AuthLabel:   a.Label,
		AuthKind:    authKind,
		Model:       model,
		Input:       counts.InputTokens,
		Output:      counts.OutputTokens,
		CacheRead:   counts.CacheReadTokens,
		CacheCreate: counts.CacheCreateTokens,
		CostUSD:     costUSD,
		Status:      resp.StatusCode,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      stream,
		Path:        path,
		Attempts:    attempts,
		UserID:      userID,
		Multiplier:  multiplier,
	})
	return false, true, nil
}

// doForwardAnthropicAPIKey is the API-key passthrough for Anthropic-shaped
// upstreams (api.anthropic.com or third-party relays). Unlike the OAuth path,
// we do not inject any Claude Code mimicry headers, do not use uTLS, and do
// not retry across credentials. Whatever the upstream returns is forwarded
// to the client verbatim — credential cooldowns and cross-credential retries
// are intentionally skipped. The only request-side change allowed is the
// per-credential model rewrite (and the matching response-side rewrite) so
// model_map'd relay vendors keep working.
//
// Health tracking: success → MarkSuccess; 401/402/403 → immediate
// MarkHardFailure (token revoked, balance depleted, or forbidden — all
// terminal signals that won't self-heal); other upstream errors
// (4xx/5xx/429) and transport errors → MarkFailure, which counts toward
// the consecutive-failure threshold and auto-promotes to a sticky
// hard-failure once the upstream proves persistently broken. A background
// midnight job (Pool.RunDailyAnthropicAPIKeyReset) wipes the hard-failure
// flag once a day so transient overnight outages don't pin a credential
// offline forever.
// The (retry, done, deferred) contract matches doForward: a credential-level
// error (401/402/403 hard-fail, 429/503 transient) is withheld and returned in
// deferred so forward() can switch credentials transparently. There is no
// bootstrap-wait gate on this path, so it takes no isRetry flag.
func (s *Server) doForwardAnthropicAPIKey(c *gin.Context, a *auth.Auth, path string, body []byte, stream bool, model, clientToken, clientName string, start time.Time, attempts int) (retry bool, done bool, deferred *deferredResponse) {
	baseURL := s.cfg.AnthropicBaseURL
	if ab := strings.TrimRight(a.Snapshot().BaseURL, "/"); ab != "" {
		baseURL = ab
	}
	upURL := baseURL + path

	upstreamBody := body
	rewriteClientModel := ""
	if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		if rewritten, err := rewriteModelField(body, upstreamModel); err == nil {
			upstreamBody = rewritten
			rewriteClientModel = model
		} else {
			log.Warnf("proxy(apikey): model rewrite (%s -> %s) failed via %s: %v", model, upstreamModel, a.ID, err)
		}
	}

	ctx := c.Request.Context()
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upURL, bytes.NewReader(upstreamBody))
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": err.Error()})
		return false, true, nil
	}
	copyForwardableHeaders(c.Request.Header, upReq.Header)
	stripIngressHeaders(upReq.Header)
	token, _ := a.Credentials()
	upReq.Header.Set("x-api-key", token)

	client := auth.ClientFor(a.ProxyURL, false)
	resp, err := client.Do(upReq)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			log.Infof("proxy(apikey): client canceled via %s: %v", a.ID, err)
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken),
				Provider: auth.NormalizeProvider(a.Provider), AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
				Model: model, Stream: stream, Path: path, Status: 499,
				DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: "client canceled",
			})
			return false, true, nil
		}
		log.Warnf("proxy(apikey): upstream transport error via %s: %v", a.ID, err)
		a.MarkFailure(fmt.Sprintf("transport: %v", err))
		c.AbortWithStatusJSON(502, gin.H{"error": err.Error()})
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken),
			Provider: auth.NormalizeProvider(a.Provider), AuthID: a.ID, AuthLabel: a.Label, AuthKind: "apikey",
			Model: model, Stream: stream, Path: path, Status: 502,
			DurationMs: time.Since(start).Milliseconds(), Attempts: attempts, Error: err.Error(),
		})
		return false, true, nil
	}

	// Decompress upstream gzip/br before reading. Some relays emit gzipped
	// 4xx error pages even when the request didn't advertise an
	// Accept-Encoding; without this the captured snippet is binary.
	maybeDecompressResponse(resp)

	// Reactive thinking-signature recovery for API-key relays. Relays that pool
	// and rotate backend accounts per request reject the echoed `thinking`
	// signatures from prior turns ("Invalid signature in thinking block",
	// returned as 400 or relay-rewrapped 500). Sanitize + replay once on the
	// same key; on success, persist strip-thinking so future requests on this
	// credential sanitize proactively (no failing first attempt). Generic across
	// every API-key provider.
	if resp.StatusCode >= 400 && path == "/v1/messages" {
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		recovered := false
		if thinkingsig.IsSignatureError(errBody) {
			sanitized := thinkingsig.SanitizeForSwitch(body)
			if !bytes.Equal(sanitized, body) {
				retryUpstream := sanitized
				if um, ok := a.ResolveUpstreamModel(model); ok && um != model && um != "" {
					if rw, e := rewriteModelField(retryUpstream, um); e == nil {
						retryUpstream = rw
					}
				}
				log.Warnf("proxy(apikey): %s returned %d signature-in-thinking — sanitizing and retrying once on same credential", a.ID, resp.StatusCode)
				if rreq, e := http.NewRequestWithContext(ctx, http.MethodPost, upURL, bytes.NewReader(retryUpstream)); e == nil {
					copyForwardableHeaders(c.Request.Header, rreq.Header)
					stripIngressHeaders(rreq.Header)
					rreq.Header.Set("x-api-key", token)
					if rresp, de := client.Do(rreq); de == nil {
						maybeDecompressResponse(rresp)
						if rresp.StatusCode < 400 {
							log.Infof("proxy(apikey): %s signature retry succeeded", a.ID)
							resp = rresp
							recovered = true
							flagStripThinking(a)
						} else {
							_ = rresp.Body.Close()
							log.Warnf("proxy(apikey): %s signature retry still %d — surfacing original error", a.ID, rresp.StatusCode)
						}
					} else {
						log.Warnf("proxy(apikey): %s signature retry transport error: %v", a.ID, de)
					}
				}
			}
		}
		if !recovered {
			resp.Body = io.NopCloser(bytes.NewReader(errBody))
		}
	}

	// Credential health bookkeeping + retryability, computed before writing
	// anything so a credential-level error can be withheld and retried on
	// another credential while the pool still has a slot.
	retryable := false
	switch {
	case resp.StatusCode < 400:
		a.MarkSuccess()
	case resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusPaymentRequired ||
		resp.StatusCode == http.StatusForbidden:
		// Token revoked, balance depleted, or forbidden — terminal for this
		// key. Hard-fail it and retry the request on another credential.
		a.MarkHardFailure(fmt.Sprintf("upstream %d", resp.StatusCode))
		retryable = true
	case resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode == http.StatusServiceUnavailable:
		// 429 per-key throttle / 503 vendor overload: transient and not a
		// verdict on the key itself, so skip Mark* (don't pin a working key or
		// trip the consecutive-429 stealth-ban accumulator) — but retry on
		// another credential so the user doesn't eat the relay's weather.
		retryable = true
	case resp.StatusCode == http.StatusNotFound:
		// Route not implemented on this relay (e.g. /v1/messages/count_tokens
		// on relays that only proxy /v1/messages). Another credential on the
		// same relay would 404 too — forward it through, don't burn retries.
	default:
		a.MarkFailure(fmt.Sprintf("upstream %d", resp.StatusCode))
	}

	if resp.StatusCode >= 400 && retryable {
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		log.Warnf("proxy(apikey): %s returned %d — retrying on another credential. body=%s", a.ID, resp.StatusCode, truncate(errBody, 500))
		return true, false, &deferredResponse{
			status: resp.StatusCode,
			header: resp.Header.Clone(),
			body:   errBody,
			authID: a.ID,
		}
	}

	writeResponseHeaders(c, resp)
	var counts usage.Counts
	var sub subUsage
	var errSnippet string
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		_, _ = c.Writer.Write(errBody)
		errSnippet = truncate(errBody, 500)
		log.Warnf("proxy(apikey): %s returned %d — body=%s", a.ID, resp.StatusCode, errSnippet)
	} else {
		counts.Requests = 1
		if stream && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			streamSSE(c, resp, &counts, &sub, rewriteClientModel)
		} else {
			respBody, _ := io.ReadAll(resp.Body)
			if rewriteClientModel != "" {
				respBody = rewriteResponseModel(respBody, rewriteClientModel)
			}
			_, _ = c.Writer.Write(respBody)
			counts.Add(extractUsageFromJSON(respBody, &sub))
		}
	}
	_ = resp.Body.Close()

	var costUSD float64
	var userID int64
	var multiplier float64
	if resp.StatusCode < 400 {
		s.usage.Record(a.ID, a.Label, counts)
		if counts.Requests > 0 && clientToken != "" {
			officialCost := s.pricing.Cost(auth.NormalizeProvider(a.Provider), model, counts)
			costUSD = officialCost
			if info, ok := saasInfoFrom(c); ok && s.saas != nil {
				billed, err := s.saas.Charge(c.Request.Context(), info, auth.NormalizeProvider(a.Provider), model, counts, officialCost)
				if err != nil {
					log.Warnf("saas: charge failed for token=%d user=%d: %v", info.TokenID, info.UserID, err)
				} else {
					costUSD = billed
					userID = info.UserID
					multiplier = s.saas.MultiplierFor(c.Request.Context(), info.GroupID, auth.NormalizeProvider(a.Provider))
				}
			}
		}
		advisorCost := s.recordSubUsage(c, a, "apikey", clientToken, clientName, model, path, resp.StatusCode, sub)
		if counts.Requests > 0 && clientToken != "" {
			var clientCounts usage.Counts
			clientCounts.Add(counts)
			for _, sc := range sub.byModel {
				clientCounts.Add(sc)
			}
			s.usage.RecordClient(clientToken, clientName, clientCounts, costUSD+advisorCost)
		}
	}
	errField := ""
	if resp.StatusCode >= 400 {
		errField = fmt.Sprintf("upstream %d: %s", resp.StatusCode, truncate([]byte(errSnippet), 200))
	}
	s.emitLog(requestlog.Record{
		Client:      clientName,
		ClientToken: maskClientToken(clientToken),
		Provider:    auth.NormalizeProvider(a.Provider),
		AuthID:      a.ID,
		AuthLabel:   a.Label,
		AuthKind:    "apikey",
		Model:       model,
		Input:       counts.InputTokens,
		Output:      counts.OutputTokens,
		CacheRead:   counts.CacheReadTokens,
		CacheCreate: counts.CacheCreateTokens,
		CostUSD:     costUSD,
		Status:      resp.StatusCode,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      stream,
		Path:        path,
		Attempts:    attempts,
		UserID:      userID,
		Multiplier:  multiplier,
		Error:       errField,
	})
	return false, true, nil
}

// stripIngressHeaders removes headers that describe the *ingress path* into
// our server before forwarding upstream. Critical when the server sits
// behind Cloudflare Tunnel: cloudflared injects Cdn-Loop: cloudflare plus a
// pile of Cf-* headers, and api.anthropic.com / chatgpt.com are themselves
// behind CF — seeing those headers triggers CF's loop-prevention WAF and
// returns 403 HTML. Prefix match so future CF additions are covered.
func stripIngressHeaders(h http.Header) {
	for k := range h {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "cf-") || strings.HasPrefix(lower, "cdn-") ||
			strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-real-") {
			h.Del(k)
		}
	}
	for _, k := range []string{"Forwarded", "Via", "Cookie", "Referer", "Origin", "True-Client-Ip"} {
		h.Del(k)
	}
}

func copyForwardableHeaders(src, dst http.Header) {
	for k, vs := range src {
		if hopHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		// Don't forward Host.
		if strings.EqualFold(k, "Host") {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func writeResponseHeaders(c *gin.Context, resp *http.Response) {
	for k, vs := range resp.Header {
		if hopHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.WriteHeader(resp.StatusCode)
}

// applyAnthropicHeaders rewrites the upstream request to look like a real
// Claude Code CLI client. Header set and values mirror upstream CLIProxyAPI's
// runtime/executor/claude_executor.go (Claude Code 2.1.63 / SDK 0.74.0).
//
// Two layers of fingerprint matter to Anthropic's edge:
//  1. TLS — handled by auth.ClientFor + utls Chrome_Auto.
//  2. HTTP headers — handled here. We must send the same User-Agent /
//     X-Stainless-* / X-App / Anthropic-Beta / X-Claude-Code-Session-Id /
//     x-client-request-id set the official client sends, otherwise the
//     application layer trivially exposes us.
//
// Client-supplied values (already populated by copyForwardableHeaders) win
// over our defaults, except for the Authorization / x-api-key pair which we
// always overwrite with credentials from the pool.
//
// Known intentional deviations:
//   - Accept-Encoding stays "identity" for both stream and non-stream because
//     our response path streams raw bytes without decompression. Real Claude
//     Code sends "gzip, deflate, br, zstd" on non-stream requests.
//
// accountAnchorFor returns a never-empty, per-credential anchor used to
// derive the device_id (and session_id) for one OAuth account. AccountKey()
// already falls back account_uuid > email > file-basename, and the basename
// is always set, so in practice this equals AccountKey() for every real
// credential — a no-op that does NOT rotate any live account's device_id.
// It exists purely as an explicit last line of defense: should every account
// field somehow be blank, anchor on the credential's stable ID rather than
// let the empty string collapse multiple credentials onto one shared
// device_id (which would make distinct accounts look like one device — a
// ban signal). Keeps the "one credential = one device_id" guarantee local
// to this package instead of depending on cc-core's ID=basename detail.
func accountAnchorFor(a *auth.Auth) string {
	if k := strings.TrimSpace(a.AccountKey()); k != "" {
		return k
	}
	if id := strings.TrimSpace(a.ID); id != "" {
		return id
	}
	return "cpa-unknown-account"
}

func applyAnthropicHeaders(req *http.Request, a *auth.Auth, stream, isAnthropicBase bool, id SimIdentity, body []byte) {
	token, kind := a.Credentials()

	// Auth header — always overwrite whatever the client sent.
	if kind == auth.KindAPIKey {
		req.Header.Del("Authorization")
		req.Header.Set("x-api-key", token)
	} else {
		req.Header.Del("x-api-key")
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")

	// Anthropic protocol headers.
	ensureHeader(req.Header, "Anthropic-Version", claudeAnthropicVersion)
	if existing := strings.TrimSpace(req.Header.Get("Anthropic-Beta")); existing != "" {
		// Client supplied its own beta list; make sure oauth marker is in it
		// when we're using OAuth (mirrors upstream behavior).
		if kind == auth.KindOAuth && !strings.Contains(existing, "oauth") {
			req.Header.Set("Anthropic-Beta", existing+",oauth-2025-04-20")
		}
	} else {
		// Pick the right captured beta list per credential kind. The OAuth
		// list contains tokens (oauth-2025-04-20, advanced-tool-use-2025-11-20,
		// cache-diagnosis-2026-04-07) that strict 3rd-party apikey gateways
		// will reject — real CC's apikey path doesn't send them either.
		if kind == auth.KindAPIKey {
			req.Header.Set("Anthropic-Beta", claudeAnthropicBetaApikey)
		} else {
			req.Header.Set("Anthropic-Beta", claudeAnthropicBetaFull)
		}
	}
	// Real CC 2.1.126 OAuth captures show this header is sent on every
	// /v1/messages, contradicting the older "OAuth never sends it" assumption.
	// Set unconditionally when targeting the first-party endpoint.
	if isAnthropicBase {
		ensureHeader(req.Header, "Anthropic-Dangerous-Direct-Browser-Access", "true")
	}

	// Stainless SDK / device profile fingerprint headers.
	ensureHeader(req.Header, "X-App", "cli")
	ensureHeader(req.Header, "X-Stainless-Retry-Count", claudeStainlessRetryCnt)
	ensureHeader(req.Header, "X-Stainless-Lang", claudeStainlessLang)
	ensureHeader(req.Header, "X-Stainless-Runtime", claudeStainlessRuntime)
	ensureHeader(req.Header, "X-Stainless-Runtime-Version", claudeStainlessRuntimeV)
	ensureHeader(req.Header, "X-Stainless-Package-Version", claudeStainlessPackageV)
	ensureHeader(req.Header, "X-Stainless-Os", claudeStainlessOS)
	ensureHeader(req.Header, "X-Stainless-Arch", claudeStainlessArch)
	ensureHeader(req.Header, "X-Stainless-Timeout", claudeStainlessTimeout)

	// Stable per-credential session ID; new UUID per request.
	ensureHeader(req.Header, "X-Claude-Code-Session-Id", SessionIDFor(id, body))
	if isAnthropicBase {
		ensureHeader(req.Header, "x-client-request-id", newRequestUUID())
	}

	// User-Agent: keep the client value if it's already a Claude Code UA,
	// otherwise overwrite with our pinned default. Mirrors upstream's legacy
	// device-profile mode (helps/claude_device_profile.go:ApplyClaudeLegacyDeviceHeaders).
	curUA := strings.TrimSpace(req.Header.Get("User-Agent"))
	if !strings.HasPrefix(curUA, "claude-cli/") {
		req.Header.Set("User-Agent", claudeCLIUserAgent)
	}

	req.Header.Set("Connection", "keep-alive")
	// Match real CC 2.1.126 — it advertises gzip,br on every request even
	// for SSE (Anthropic's edge typically doesn't compress text/event-stream
	// anyway, but the advertise is part of the fingerprint). Response-side
	// decompression for non-stream paths is handled by maybeDecompressResponse.
	req.Header.Set("Accept-Encoding", "gzip, br")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		ensureHeader(req.Header, "Accept", "application/json")
	}
}

// maybeDecompressResponse swaps resp.Body for a transparent decoder when
// upstream returned a gzip/br body, then strips Content-Encoding /
// Content-Length so the response we forward to the client is plain text.
// No-op when Content-Encoding is empty/identity (most SSE responses).
func maybeDecompressResponse(resp *http.Response) {
	enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if enc == "" || enc == "identity" {
		return
	}
	switch enc {
	case "gzip":
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			log.Warnf("proxy: gzip decoder init failed: %v (forwarding compressed body)", err)
			return
		}
		resp.Body = &decompressedBody{rc: gz, underlying: resp.Body}
	case "br":
		br := brotli.NewReader(resp.Body)
		resp.Body = &decompressedBody{rc: io.NopCloser(br), underlying: resp.Body}
	default:
		// Unknown encoding (deflate, zstd, etc.) — pass through unchanged.
		// Anthropic doesn't currently send these for /v1/messages.
		return
	}
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
}

// decompressedBody chains a decompressor's Close to the underlying body.
type decompressedBody struct {
	rc         io.ReadCloser
	underlying io.ReadCloser
}

func (d *decompressedBody) Read(p []byte) (int, error) { return d.rc.Read(p) }
func (d *decompressedBody) Close() error {
	_ = d.rc.Close()
	return d.underlying.Close()
}

// streamSSE copies SSE events to the client as they arrive and parses
// message_delta events to accumulate usage. When rewriteClientModel is
// non-empty, each data: JSON has its top-level "model" and nested
// "message.model" fields rewritten to that value before being forwarded.
func streamSSE(c *gin.Context, resp *http.Response, counts *usage.Counts, sub *subUsage, rewriteClientModel string) {
	flusher, _ := c.Writer.(http.Flusher)
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	var curEvent string
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			trim := bytes.TrimRight(line, "\r\n")
			outLine := line
			if bytes.HasPrefix(trim, []byte("event:")) {
				curEvent = strings.TrimSpace(string(trim[6:]))
			} else if bytes.HasPrefix(trim, []byte("data:")) {
				payload := bytes.TrimSpace(trim[5:])
				if rewriteClientModel != "" && len(payload) > 0 && payload[0] == '{' {
					if rewritten := rewriteResponseModel(payload, rewriteClientModel); rewritten != nil {
						// Preserve the original line's trailing newline style.
						tail := line[len(trim):]
						rebuilt := make([]byte, 0, len("data: ")+len(rewritten)+len(tail))
						rebuilt = append(rebuilt, []byte("data: ")...)
						rebuilt = append(rebuilt, rewritten...)
						rebuilt = append(rebuilt, tail...)
						outLine = rebuilt
					}
				}
				if curEvent == "message_start" || curEvent == "message_delta" {
					mergeSSEUsage(counts, sub, payload)
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

type usageJSON struct {
	InputTokens              int64               `json:"input_tokens"`
	OutputTokens             int64               `json:"output_tokens"`
	CacheCreationInputTokens int64               `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64               `json:"cache_read_input_tokens"`
	Iterations               []iterationUsageRaw `json:"iterations,omitempty"`
}

// iterationUsageRaw is the shape of one entry inside `usage.iterations[]`,
// added by the `advisor-tool-2026-03-01` beta. Each entry is one billable
// sub-call inside a single /v1/messages request:
//
//	type:"message"          → orchestrator (the model the client asked for).
//	                          Top-level usage is the SUM of these — already
//	                          accounted for; we ignore them here.
//	type:"advisor_message"  → server-side advisor call, billed under its own
//	                          model (typically claude-opus-4-7), NOT rolled
//	                          into top-level totals.
//
// We only care about the second kind. cache_read/cache_create are typically
// 0 for advisor (each call re-reads the transcript fresh) but we keep all
// four counters so the price formula stays correct if Anthropic enables
// caching for advisor later.
type iterationUsageRaw struct {
	Type                     string `json:"type"`
	Model                    string `json:"model"`
	InputTokens              int64  `json:"input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
}

func (u usageJSON) toCounts() usage.Counts {
	return usage.Counts{
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CacheCreateTokens: u.CacheCreationInputTokens,
		CacheReadTokens:   u.CacheReadInputTokens,
	}
}

// subUsage carries advisor (and any future server-side sub-model) counts
// alongside a request, keyed by upstream model name. A request with no
// advisor invocation leaves it nil/empty.
type subUsage struct {
	// byModel sums per-model counts across all iterations of that model.
	// Most requests have at most one entry ("claude-opus-4-7").
	byModel map[string]usage.Counts
}

func (s *subUsage) merge(it iterationUsageRaw) {
	if it.Type != "advisor_message" {
		return
	}
	model := strings.TrimSpace(it.Model)
	if model == "" {
		// Defensive: should never happen, but if Anthropic ever emits an
		// advisor iteration without a model field, charge it to a sentinel
		// so it's visible in the admin UI rather than silently dropped.
		model = "advisor-unknown"
	}
	if s.byModel == nil {
		s.byModel = make(map[string]usage.Counts, 1)
	}
	cur := s.byModel[model]
	cur.InputTokens += it.InputTokens
	cur.OutputTokens += it.OutputTokens
	cur.CacheCreateTokens += it.CacheCreationInputTokens
	cur.CacheReadTokens += it.CacheReadInputTokens
	s.byModel[model] = cur
}

// replaceFrom resets the per-model totals from a full iterations slice. SSE
// emits cumulative `message_delta.usage.iterations` (the slice grows as
// sub-calls complete), so we overwrite rather than append to avoid double-
// counting when both message_start and message_delta are observed.
func (s *subUsage) replaceFrom(its []iterationUsageRaw) {
	if len(its) == 0 {
		return
	}
	s.byModel = nil
	for _, it := range its {
		s.merge(it)
	}
}

// recordSubUsage charges advisor (and any future server-side sub-model)
// counts to the same auth that handled the parent request, and emits one
// extra requestlog row per distinct sub-model so by-model aggregation in
// the admin panel separates orchestrator cost from advisor cost.
//
// Returns the total advisor USD cost so the caller can fold it into the
// per-client weekly ledger as a single sum (one /v1/messages call = one
// weekly Requests bump regardless of how many sub-models ran).
//
// No-op when the response is an error (status >= 400) or there are no
// advisor iterations. Auth-side load tracking only applies to successful
// sub-calls — a failed parent rarely has billable advisor activity, and
// double-counting would distort WeightedTotal-driven load balancing.
func (s *Server) recordSubUsage(c *gin.Context, a *auth.Auth, authKind, clientToken, clientName, _ string, path string, status int, sub subUsage) float64 {
	if status >= 400 || len(sub.byModel) == 0 {
		return 0
	}
	provider := auth.NormalizeProvider(a.Provider)
	var total float64
	info, hasSaaS := saasInfoFrom(c)
	var subUserID int64
	var subMultiplier float64
	if hasSaaS && s.saas != nil {
		subUserID = info.UserID
		subMultiplier = s.saas.MultiplierFor(c.Request.Context(), info.GroupID, provider)
	}
	for subModel, sc := range sub.byModel {
		// Sub-calls bump the auth's daily/hourly bucket and WeightedTotal so
		// the credential bears the full opus load. Requests stays 0: the
		// parent already counted +1.
		s.usage.Record(a.ID, a.Label, sc)
		official := s.pricing.Cost(provider, subModel, sc)
		// Advisor cost goes through the same Charge funnel as the parent
		// request so the wallet ledger and the request log agree on what
		// the user actually paid for the sub-call.
		cost := official
		if hasSaaS && s.saas != nil {
			if billed, err := s.saas.Charge(c.Request.Context(), info, provider, subModel, sc, official); err == nil {
				cost = billed
			}
		}
		total += cost
		s.emitLog(requestlog.Record{
			Client:      clientName,
			ClientToken: maskClientToken(clientToken),
			Provider:    provider,
			AuthID:      a.ID,
			AuthLabel:   a.Label,
			AuthKind:    authKind,
			Model:       subModel,
			Input:       sc.InputTokens,
			Output:      sc.OutputTokens,
			CacheRead:   sc.CacheReadTokens,
			CacheCreate: sc.CacheCreateTokens,
			CostUSD:     cost,
			Status:      status,
			// DurationMs/Stream/Attempts intentionally zero: this row is a
			// sub-call summary, not an independent request — adding wall
			// time would double-count it in admin's "total time" stats.
			Path:       path + "#advisor:" + subModel,
			UserID:     subUserID,
			Multiplier: subMultiplier,
		})
	}
	return total
}

// extractUsageFromJSON pulls the top-level "usage" from a non-streaming
// /v1/messages response. Advisor sub-billing iterations are folded into
// `sub` if non-nil.
func extractUsageFromJSON(body []byte, sub *subUsage) usage.Counts {
	var wrap struct {
		Usage usageJSON `json:"usage"`
	}
	_ = json.Unmarshal(body, &wrap)
	if sub != nil {
		sub.replaceFrom(wrap.Usage.Iterations)
	}
	return wrap.Usage.toCounts()
}

// mergeSSEUsage overlays usage fields from a single Anthropic SSE data
// payload onto dst, using overwrite-if-positive semantics. This is NOT
// additive: Anthropic's stream sends the input/cache token baseline in
// message_start and the cumulative final usage (often repeating the same
// input/cache values plus the real output count) in message_delta, so
// summing the two events would double-count input and cache tokens.
//
// Shapes handled:
//
//	message_start:  {type: "message_start", message: {usage: {...}}}
//	message_delta:  {type: "message_delta", usage: {...}}
//
// Zero values from a later event don't clobber a prior non-zero value —
// matches the protocol where message_delta sometimes omits the input
// fields (e.g. emits input_tokens=0).
func mergeSSEUsage(dst *usage.Counts, sub *subUsage, payload []byte) {
	if dst == nil {
		return
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(payload, &probe); err != nil {
		return
	}
	var u usageJSON
	if raw, ok := probe["usage"]; ok {
		_ = json.Unmarshal(raw, &u)
	} else if raw, ok := probe["message"]; ok {
		var nested struct {
			Usage usageJSON `json:"usage"`
		}
		if err := json.Unmarshal(raw, &nested); err == nil {
			u = nested.Usage
		} else {
			return
		}
	} else {
		return
	}
	if u.InputTokens > 0 {
		dst.InputTokens = u.InputTokens
	}
	if u.OutputTokens > 0 {
		dst.OutputTokens = u.OutputTokens
	}
	if u.CacheCreationInputTokens > 0 {
		dst.CacheCreateTokens = u.CacheCreationInputTokens
	}
	if u.CacheReadInputTokens > 0 {
		dst.CacheReadTokens = u.CacheReadInputTokens
	}
	if sub != nil && len(u.Iterations) > 0 {
		// message_delta.usage.iterations is cumulative — last non-empty
		// observation wins, never append.
		sub.replaceFrom(u.Iterations)
	}
}

func parseRetryAfter(h http.Header) time.Time {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return time.Time{}
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Now().Add(time.Duration(n) * time.Second)
	}
	if t, err := http.ParseTime(v); err == nil {
		return t
	}
	return time.Time{}
}

// parseUnifiedRatelimitRejected reports whether Anthropic's
// `anthropic-ratelimit-unified-*` headers signal that this credential is out
// of quota right now. When yes, the returned time is when to try again
// (parsed from `*-reset`, expressed as Unix seconds; falls back 1h ahead if
// missing/unparseable).
//
// Real responses carry a snapshot like:
//
//	anthropic-ratelimit-unified-status: rejected           ← top-level decision
//	anthropic-ratelimit-unified-5h-status: rejected        ← per-bucket states
//	anthropic-ratelimit-unified-7d-status: allowed
//	anthropic-ratelimit-unified-5h-reset: 1777824000       ← per-bucket reset
//	anthropic-ratelimit-unified-7d-reset: 1778018400
//	anthropic-ratelimit-unified-reset: 1777824000          ← top-level reset
//	anthropic-ratelimit-unified-representative-claim: five_hour
//
// We treat ANY of `unified-status / unified-5h-status / unified-7d-status`
// being "rejected" (or a prefix thereof, e.g. "rejected_*") as a quota signal.
// For the reset time we prefer top-level `unified-reset`, else the latest of
// the rejected per-bucket resets — that way a 7d rejection isn't released by
// the (sooner) 5h reset.
//
// Returns:
//   - ok=false: not rejected.
//   - ok=true, banned=true: rejected but no usable FUTURE reset stamp
//     (every parseable stamp is in the past, or there is no stamp at all).
//     This shape — "you're rejected, recovery time = unknown / already past"
//     — is the stealth-ban signature: a banned account stays "rejected"
//     forever, so admin-panel users see "Quota exceeded → resets in now"
//     looping. Caller must hard-fail the credential.
//   - ok=true, banned=false: rejected with a real future reset stamp →
//     normal quota cooldown until that time.
func parseUnifiedRatelimitRejected(h http.Header) (resetAt time.Time, banned bool, ok bool) {
	const statusPrefix = "rejected"
	isRejected := func(headerName string) bool {
		v := strings.ToLower(strings.TrimSpace(h.Get(headerName)))
		return v != "" && strings.HasPrefix(v, statusPrefix)
	}

	bucketStatuses := []struct{ statusHdr, resetHdr string }{
		{"Anthropic-Ratelimit-Unified-5h-Status", "Anthropic-Ratelimit-Unified-5h-Reset"},
		{"Anthropic-Ratelimit-Unified-7d-Status", "Anthropic-Ratelimit-Unified-7d-Reset"},
	}
	rejected := isRejected("Anthropic-Ratelimit-Unified-Status")
	var bucketReset time.Time
	for _, b := range bucketStatuses {
		if !isRejected(b.statusHdr) {
			continue
		}
		rejected = true
		if t, parsed := parseUnixSecondsHeader(h.Get(b.resetHdr)); parsed && t.After(bucketReset) {
			bucketReset = t
		}
	}
	if !rejected {
		return time.Time{}, false, false
	}
	now := time.Now()
	if t, parsed := parseUnixSecondsHeader(h.Get("Anthropic-Ratelimit-Unified-Reset")); parsed && t.After(now) {
		return clampReset(t), false, true
	}
	if !bucketReset.IsZero() && bucketReset.After(now) {
		return clampReset(bucketReset), false, true
	}
	// Rejected with no future reset (every stamp is past, or missing entirely).
	// This is what a banned subscription account looks like — Anthropic flags
	// it "rejected" forever with no recovery time. Tell caller to hard-fail.
	return time.Time{}, true, true
}

// parseUnixSecondsHeader parses an `epoch-seconds` integer header value into
// a time.Time. Tolerates whitespace; returns ok=false on empty / non-integer.
func parseUnixSecondsHeader(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(v, 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

// clampReset caps a parsed future reset stamp at 30 days as a defense
// against malformed payloads. Caller is responsible for ensuring t is
// already in the future (past stamps are a separate signal — see
// parseUnifiedRatelimitRejected).
func clampReset(t time.Time) time.Time {
	maxV := time.Now().Add(30 * 24 * time.Hour)
	if t.After(maxV) {
		return maxV
	}
	return t
}

// parseClaudeUsageLimitBody extracts the reset timestamp from a Claude
// subscription usage-limit 429. Anthropic encodes it as
// "Claude AI usage limit reached|<unix-seconds>" in the message field, e.g.
//
//	{"type":"error","error":{"type":"rate_limit_error",
//	  "message":"Claude AI usage limit reached|1714761600"}}
//
// ok=true means we found the marker AND parsed a sane future timestamp;
// caller should treat this as a regular quota cooldown (NOT a stealth ban),
// because the body is explicit about both the cause and the recovery time.
func parseClaudeUsageLimitBody(b []byte) (time.Time, bool) {
	if len(b) == 0 {
		return time.Time{}, false
	}
	const marker = "Claude AI usage limit reached"
	lower := bytes.ToLower(b)
	idx := bytes.Index(lower, []byte(strings.ToLower(marker)))
	if idx < 0 {
		return time.Time{}, false
	}
	// Walk past the marker in the original (non-lowercased) body; we want
	// the literal "|<digits>" tail. Tolerate optional whitespace.
	tail := b[idx+len(marker):]
	for len(tail) > 0 && (tail[0] == ' ' || tail[0] == '\t') {
		tail = tail[1:]
	}
	if len(tail) == 0 || tail[0] != '|' {
		// Marker present but no timestamp — still a usage-limit signal,
		// but we have nothing to set the cooldown to. Fall back to a
		// best-effort 1h cooldown so the credential doesn't loop.
		return time.Now().Add(1 * time.Hour), true
	}
	tail = tail[1:]
	end := 0
	for end < len(tail) && tail[end] >= '0' && tail[end] <= '9' {
		end++
	}
	if end == 0 {
		return time.Now().Add(1 * time.Hour), true
	}
	secs, err := strconv.ParseInt(string(tail[:end]), 10, 64)
	if err != nil {
		return time.Now().Add(1 * time.Hour), true
	}
	t := time.Unix(secs, 0)
	// Reject obviously bogus timestamps (already passed or > 30 days out)
	// — degrade to the 1h fallback so we don't park a credential forever
	// on a malformed payload.
	if t.Before(time.Now()) || t.After(time.Now().Add(30*24*time.Hour)) {
		return time.Now().Add(1 * time.Hour), true
	}
	return t, true
}

// isAccountBanBody reports whether the upstream error body looks like a
// terminal account/organization ban from Anthropic. Match is case-insensitive
// and deliberately narrow to avoid firing on routine rate-limit / usage-limit
// copy (e.g. "your organization's usage limit").
func isAccountBanBody(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	lower := bytes.ToLower(b)
	markers := [][]byte{
		[]byte("organization has been disabled"),
		[]byte("account has been disabled"),
		[]byte("account is disabled"),
		[]byte("organization is disabled"),
		// Org-level OAuth revocation. Anthropic returns a 403
		// permission_error with this exact wording when the
		// subscription account has been blocked from using OAuth
		// (typically a stealth/soft ban). Recovery requires manual
		// intervention, not a cooldown — treat as terminal.
		[]byte("oauth authentication is currently not allowed"),
	}
	for _, m := range markers {
		if bytes.Contains(lower, m) {
			return true
		}
	}
	return false
}

// isDefinitiveAuthRejection reports whether a 401 response body indicates
// Anthropic has terminally rejected the credential (subscription revoked,
// account dead, Opus access stripped). Distinguished from transient 401s
// (which we'd want to cooldown-and-retry) by matching only the explicit
// authentication-error wording: `error.type == "authentication_error"` is
// reserved by Anthropic for credential-validity failures and never used
// for quota / permission / request-shape issues. Caller must also have
// confirmed status == 401 — at other statuses these markers can appear
// in benign contexts.
func isDefinitiveAuthRejection(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	lower := bytes.ToLower(b)
	markers := [][]byte{
		[]byte(`"type":"authentication_error"`),
		[]byte(`"type": "authentication_error"`),
		[]byte("invalid authentication credentials"),
	}
	for _, m := range markers {
		if bytes.Contains(lower, m) {
			return true
		}
	}
	return false
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}

// rewriteModelField returns a copy of the JSON request body with its top-level
// "model" string set to upstream. Used when an API-key credential has a
// model_map entry that rewrites the client's model name to a vendor-specific
// one (e.g. "claude-opus-4-6" -> "[0.16]稳定喵/claude-opus-4-6"). Falls back
// to leaving the body alone if the JSON can't be parsed as an object.
func rewriteModelField(body []byte, upstream string) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return body, nil
	}
	mb, err := json.Marshal(upstream)
	if err != nil {
		return nil, err
	}
	obj["model"] = mb
	return json.Marshal(obj)
}

// rewriteResponseModel substitutes the client-facing model name into the
// response JSON so the client never sees the relay vendor's prefixed name
// (e.g. "[0.16]稳定喵/claude-opus-4-6"). Handles both the non-streaming
// /v1/messages response (top-level "model") and SSE event payloads
// (message_start nests "message.model"). Returns the original bytes if
// parsing fails or no known model path is present.
func rewriteResponseModel(data []byte, clientModel string) []byte {
	if len(data) == 0 || clientModel == "" {
		return data
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return data
	}
	changed := false
	newModel, err := json.Marshal(clientModel)
	if err != nil {
		return data
	}
	if _, ok := obj["model"]; ok {
		obj["model"] = newModel
		changed = true
	}
	if raw, ok := obj["message"]; ok && len(raw) > 0 && raw[0] == '{' {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(raw, &inner); err == nil {
			if _, ok := inner["model"]; ok {
				inner["model"] = newModel
				if merged, err := json.Marshal(inner); err == nil {
					obj["message"] = merged
					changed = true
				}
			}
		}
	}
	if !changed {
		return data
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return out
}

// unused — kept to avoid import churn if future error types are added.
var _ = fmt.Sprintf
var _ = context.Background
