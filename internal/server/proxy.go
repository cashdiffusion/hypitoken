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
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/advisor"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/sidecar"
	ccstream "github.com/wjsoj/cc-core/stream"
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
		writeAPIError(c, provider, APIError{Status: http.StatusBadRequest, Code: "invalid_request_body", Message: "The request body could not be read. Send valid JSON and try again."})
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
			writeAPIError(c, provider, APIError{Status: pre.Status, Code: pre.Code, Message: pre.Message, Details: pre.Details})
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
				writeAPIError(c, provider, APIError{Status: http.StatusTooManyRequests, Code: "api_key_weekly_limit_exceeded", Message: "This API key has reached its weekly spending limit. Increase the key limit or retry after the weekly reset.", Details: map[string]any{"spent_usd": spent, "limit_usd": weeklyLimit, "week": s.usage.CurrentWeekKey()}})
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
		writeAPIError(c, provider, APIError{Status: http.StatusBadRequest, Code: "unsupported_endpoint", Message: msg})
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
	if limit := s.clientRPM(c, provider, clientToken); limit > 0 {
		if ok, retry := s.rpm.Allow(rpmKey, limit); !ok {
			c.Header("Retry-After", strconv.Itoa(retry))
			writeAPIError(c, provider, APIError{Status: http.StatusTooManyRequests, Code: "api_key_rate_limit_exceeded", Message: "This API key has exceeded its requests-per-minute limit. Retry after the indicated delay.", Details: map[string]any{"rpm_limit": limit, "retry_after_seconds": retry}})
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
	maxConc := s.clientMaxConcurrent(c, provider, clientToken)
	if maxConc > 0 {
		// Scope the counter per provider so Claude and Codex share a token
		// but not a concurrency bucket — matches the per-provider session
		// keying in Pool.Acquire.
		inflightKey := auth.NormalizeProvider(provider) + "|" + clientToken
		cur, releaseSlot := s.inflight.Begin(inflightKey)
		defer releaseSlot()
		if int(cur) > maxConc {
			c.Header("Retry-After", "5")
			writeAPIError(c, provider, APIError{Status: http.StatusTooManyRequests, Code: "api_key_concurrency_limit_exceeded", Message: "This API key has too many requests in progress. Wait for an active request to finish and try again.", Details: map[string]any{"max_concurrent": maxConc, "in_flight": int(cur)}})
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

	// Convert withheld service errors to the gateway's stable public taxonomy.
	// Raw bodies remain in operator logs because they may identify a vendor,
	// relay, account, or credential.
	surfaceDeferred := func(d *deferredResponse) {
		copySafeRetryHeaders(c, d.header)
		writeAPIError(c, provider, publicUpstreamError(d.status, d.body))
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider,
			AuthID: d.authID, AuthLabel: d.authLabel, AuthKind: d.authKind,
			Model: model, Status: d.status, Attempts: attempts,
			Stream: stream, Path: path, DurationMs: time.Since(start).Milliseconds(),
			Error:       fmt.Sprintf("upstream %d (all credentials exhausted)", d.status),
			ClaudeAudit: d.claudeAudit,
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
			writeAPIError(c, provider, APIError{Status: http.StatusServiceUnavailable, Code: "service_temporarily_unavailable", Message: "The requested model is temporarily unavailable. Please try again shortly."})
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
	writeAPIError(c, provider, APIError{Status: http.StatusServiceUnavailable, Code: "service_temporarily_unavailable", Message: "The requested model is temporarily unavailable after several attempts. Please try again shortly."})
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
	status      int
	header      http.Header
	body        []byte
	authID      string
	authLabel   string
	authKind    string
	claudeAudit *requestlog.ClaudeAudit
}

func copySafeRetryHeaders(c *gin.Context, h http.Header) {
	for _, key := range []string{"Retry-After", "X-RateLimit-Reset"} {
		if value := h.Get(key); value != "" {
			c.Header(key, value)
		}
	}
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
	if a.Kind == auth.KindAPIKey {
		// API-key relays keep the established thinking-signature behavior. The
		// genuine preserve/rewrite policy applies only to Anthropic OAuth.
		if path == "/v1/messages" {
			switched := s.switchTracker.Check(clientToken, body, a.ID)
			if switched || a.StripThinkingEnabled() {
				if switched {
					log.Infof("auth switch detected: clientToken=%s now on auth=%s — sanitizing prior thinking signatures",
						maskClientToken(clientToken), a.ID)
				}
				body = thinkingsig.SanitizeForSwitch(body)
			}
		}
		return s.doForwardAnthropicAPIKey(c, a, path, body, stream, model, clientToken, clientName, start, attempts)
	}

	accountKey := a.AccountKey()
	var requestPolicy mimicry.RequestPolicy
	rewrite := false
	if path == "/v1/messages" {
		requestClass := mimicry.ClassifyClaudeCodeRequest(body)
		if requestClass == mimicry.RequestClassGenuine {
			genuineMode, err := genuineModeForAuth(a)
			if err != nil {
				log.Errorf("proxy: invalid Claude identity mode via %s: %v", a.ID, err)
				writeAPIError(c, auth.NormalizeProvider(a.Provider), APIError{Status: http.StatusInternalServerError, Code: "request_preparation_failed", Message: "The request could not be prepared. Please try again."})
				return false, true, nil
			}
			rewrite = genuineMode == mimicry.GenuineRequestRewrite
		}

		// Preserve is the experiment's control arm: it deliberately runs the
		// exact pre-policy request path below, including the legacy credential-ID
		// switch key, thinking sanitization, body shaping, headers, and reactive
		// signature recovery. Only rewrite enters the new prepared path.
		switchKey := a.ID
		if rewrite {
			var err error
			requestPolicy, err = mimicry.NewClaudeCodeRequestPolicy(requestClass, mimicry.GenuineRequestRewrite)
			if err != nil {
				log.Warnf("proxy: classify Claude request via %s: %v", a.ID, err)
				writeAPIError(c, auth.NormalizeProvider(a.Provider), APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "The request body is not a valid Claude messages request."})
				return false, true, nil
			}
			if strings.TrimSpace(c.GetHeader("Anthropic-Beta")) == "" {
				// Main, 1M, and title requests carry different feature vectors. With
				// no genuine downstream vector there is no safe 2.1.220 default.
				writeAPIError(c, auth.NormalizeProvider(a.Provider), APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "A genuine Claude Code request must include Anthropic-Beta."})
				return false, true, nil
			}
			// Thinking signatures are tied to the real Anthropic account, not a
			// credential filename, in the experimental identity-mapped path.
			switchKey = accountKey
		}

		switched := s.switchTracker.Check(clientToken, body, switchKey)
		if switched || a.StripThinkingEnabled() {
			if switched {
				log.Infof("auth switch detected: clientToken=%s now on auth=%s — sanitizing prior thinking signatures",
					maskClientToken(clientToken), a.ID)
			}
			body = thinkingsig.SanitizeForSwitch(body)
		}
	}

	baseURL := s.cfg.AnthropicBaseURL
	// Per-credential base URL override (used for relay/midstream vendors on
	// API-key credentials).
	if ab := strings.TrimRight(a.Snapshot().BaseURL, "/"); ab != "" {
		baseURL = ab
	}
	url := baseURL + path + "?beta=true"
	isAnthropicBase := strings.HasPrefix(strings.ToLower(baseURL), "https://api.anthropic.com")

	upstreamBody := body
	id := mimicry.SimIdentity{
		AccountKey:  accountKey,
		AccountUUID: a.AccountUUIDValue(),
		ClientToken: clientToken,
	}
	var prepared mimicry.BodyTransformResult
	if rewrite {
		var err error
		prepared, err = prepareClaudeRewriteBody(body, model, a, id, requestPolicy)
		if err != nil {
			// Local preparation failures are neither credential failures nor a
			// reason to fail over. In particular, rewrite never degrades to
			// preserve with a stale downstream identity.
			log.Errorf("proxy: prepare Claude request via %s: %v", a.ID, err)
			writeAPIError(c, auth.NormalizeProvider(a.Provider), APIError{Status: http.StatusInternalServerError, Code: "request_preparation_failed", Message: "The request could not be prepared. Please try again."})
			return false, true, nil
		}
		upstreamBody = prepared.Body()
	} else if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		// Legacy path: preserve the pre-policy model rewrite behavior, including
		// its best-effort fallback when a body cannot be decoded.
		if rewritten, err := rewriteModelField(body, upstreamModel); err == nil {
			upstreamBody = rewritten
		} else {
			log.Warnf("proxy: model rewrite (%s -> %s) failed via %s: %v", model, upstreamModel, a.ID, err)
		}
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
	if path == "/v1/messages" && !rewrite {
		// Exact legacy/control path. Genuine Claude Code requests are recognized
		// inside ApplyClaudeCodeBodyMimicry and retain their existing non-zero
		// cch, while every other legacy shaping rule stays unchanged.
		upstreamBody = mimicry.ApplyClaudeCodeBodyMimicry(upstreamBody, model, id)
		if normalized, changed := mimicry.NormalizeDateline(upstreamBody); changed {
			upstreamBody = normalized
		}
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
		case <-time.After(sidecar.BootstrapWaitCap):
			log.Warnf("sidecar: bootstrap-wait timeout for %s — proceeding without preceding bootstrap traffic", a.ID)
		}
	}
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(upstreamBody))
	if err != nil {
		writeAPIError(c, auth.NormalizeProvider(a.Provider), APIError{Status: http.StatusInternalServerError, Code: "request_preparation_failed", Message: "The request could not be prepared. Please try again."})
		return false, true, nil
	}

	// Forward selected client headers.
	copyForwardableHeaders(c.Request.Header, upReq.Header)
	stripIngressHeaders(upReq.Header)

	// Only rewrite consumes the new atomic prepared result. Preserve and
	// all non-genuine traffic continue through the exact legacy header adapter.
	if rewrite {
		if err := applyAnthropicPreparedHeaders(upReq, a, stream, isAnthropicBase, prepared); err != nil {
			log.Errorf("proxy: prepare Claude headers via %s: %v", a.ID, err)
			writeAPIError(c, auth.NormalizeProvider(a.Provider), APIError{Status: http.StatusInternalServerError, Code: "request_preparation_failed", Message: "The request could not be prepared. Please try again."})
			return false, true, nil
		}
	} else {
		applyAnthropicHeaders(upReq, a, stream, isAnthropicBase, id, upstreamBody)
	}

	client := auth.ClientFor(a.ProxyURL, s.cfg.UseUTLS)
	resp, err := client.Do(upReq)
	if err != nil {
		claudeAudit := claudeIdentityAudit(prepared, accountKey)
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
				ClaudeAudit: claudeAudit,
			})
			return false, true, nil
		}
		a.MarkFailure(err.Error())
		log.Warnf("proxy: upstream error via %s: %v", a.ID, err)
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.NormalizeProvider(a.Provider),
			AuthID: a.ID, AuthLabel: a.Label, AuthKind: "oauth", Model: model, Status: http.StatusBadGateway,
			Stream: stream, Path: path, Attempts: attempts, DurationMs: time.Since(start).Milliseconds(),
			Error: "upstream transport error", AttemptOnly: true, ClaudeAudit: claudeAudit,
		})
		return true, false, &deferredResponse{
			status: http.StatusBadGateway, authID: a.ID, authLabel: a.Label, authKind: "oauth", claudeAudit: claudeAudit,
		}
	}

	// Decompress upstream gzip/br before reading anything — we asked for
	// gzip,br to match the real CC fingerprint, but every internal path
	// (usage parsing, SSE streamer, model rewrite, body forwarding) wants
	// plain bytes. The Content-Encoding header is also stripped so the
	// client receives identity even though upstream sent compressed.
	ccstream.Decompress(resp)

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
				var retryPrepared mimicry.BodyTransformResult
				if rewrite {
					var prepErr error
					retryPrepared, prepErr = prepareClaudeRewriteBody(sanitized, model, a, id, requestPolicy)
					if prepErr != nil {
						log.Warnf("proxy: %s signature retry preparation failed: %v", a.ID, prepErr)
						goto signatureRecoveryDone
					}
					retryUpstream = retryPrepared.Body()
				} else {
					// Exact legacy/control recovery path.
					if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
						if rewritten, rewriteErr := rewriteModelField(retryUpstream, upstreamModel); rewriteErr == nil {
							retryUpstream = rewritten
						}
					}
					retryUpstream = mimicry.ApplyClaudeCodeBodyMimicry(retryUpstream, model, id)
					if normalized, changed := mimicry.NormalizeDateline(retryUpstream); changed {
						retryUpstream = normalized
					}
				}
				log.Warnf("proxy: %s returned 400 signature-in-thinking — sanitizing and retrying once on same credential", a.ID)
				if retryReq, rerr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(retryUpstream)); rerr == nil {
					copyForwardableHeaders(c.Request.Header, retryReq.Header)
					stripIngressHeaders(retryReq.Header)
					if rewrite {
						if headerErr := applyAnthropicPreparedHeaders(retryReq, a, stream, isAnthropicBase, retryPrepared); headerErr != nil {
							log.Warnf("proxy: %s signature retry header preparation failed: %v", a.ID, headerErr)
							goto signatureRecoveryDone
						}
					} else {
						applyAnthropicHeaders(retryReq, a, stream, isAnthropicBase, id, retryUpstream)
					}
					if retryResp, rderr := client.Do(retryReq); rderr == nil {
						ccstream.Decompress(retryResp)
						if retryResp.StatusCode < 400 {
							log.Infof("proxy: %s signature retry succeeded", a.ID)
							resp = retryResp
							prepared = retryPrepared
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
	signatureRecoveryDone:
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
		case resp.StatusCode == 429 && isLongContextRejection(errBody):
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
			if resetAt, scope, banned, ok := parseUnifiedRatelimitRejected(resp.Header, model); ok && !banned {
				if scope != "" {
					// Model-scoped rejection (fable's weekly allotment): cool down
					// only this model family so the credential keeps serving every
					// other model instead of being flagged account-wide.
					a.MarkModelRateLimited(scope, resetAt)
					log.Warnf("auth: %s model-scoped limit (%s) — cooldown until %s", a.ID, scope, resetAt.Format(time.RFC3339))
				} else {
					a.MarkUsageLimitReached(resetAt)
					log.Warnf("auth: %s usage limit (unified-ratelimit rejected) — cooldown until %s", a.ID, resetAt.Format(time.RFC3339))
				}
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
			// A 401 with body {"type":"authentication_error", ...} /
			// "Invalid authentication credentials" is Anthropic rejecting this
			// credential — but a SINGLE one is almost never a dead account. A
			// proactive EnsureFresh mints a new access token and Anthropic
			// invalidates the old one server-side the instant refresh
			// completes; any request that captured the old bearer and is still
			// on the wire during that ~1-2s rotation window comes back 401. On
			// a busy account (many client tokens, always some request in
			// flight) every refresh orphans one or a few requests this way —
			// all transient, all followed by successes on the fresh token.
			// Immediately hard-disabling on the first such 401 (as we used to)
			// took healthy paying subscriptions offline until a manual
			// re-login. Instead cooldown-and-retry per strike and let the
			// dedicated Consecutive401s accumulator promote to a sticky
			// hard-failure only after a sustained run with no intervening
			// success (auth401HardFailureThreshold). A genuinely revoked
			// refresh token is still caught earlier and authoritatively by the
			// refresh path's invalid_grant hard-failure.
			if resp.StatusCode == 401 && isDefinitiveAuthRejection(errBody) {
				n := a.MarkAuthRejection(fmt.Sprintf("upstream 401 authentication rejected: %s", truncate(errBody, 200)))
				if a.IsHardFailed() {
					log.Warnf("auth: %s hard-disabled — %d consecutive 401s with no success (presumed revoked)", a.ID, n)
				} else {
					// Transient (token-rotation race). Short cooldown so it steps
					// out of rotation briefly, then self-recovers on the fresh token.
					s.pool.ReportUpstreamError(a, resp.StatusCode, time.Time{})
					log.Warnf("auth: %s transient 401 (rotation race, strike %d) — cooldown + retry", a.ID, n)
				}
			} else {
				resetAt := parseRetryAfter(resp.Header)
				s.pool.ReportUpstreamError(a, resp.StatusCode, resetAt)
			}
		case resp.StatusCode == 529, resp.StatusCode >= 500:
			a.MarkFailure(fmt.Sprintf("upstream %d", resp.StatusCode))
		}
		claudeAudit := claudeIdentityAudit(prepared, accountKey)
		if claudeAudit != nil {
			claudeAudit.CredentialHardFailed = a.IsHardFailed()
		}

		authKind := "oauth"
		if a.Kind == auth.KindAPIKey {
			authKind = "apikey"
		}
		// Break sticky session so the retry (and any future request from this
		// client) can be assigned to a different, hopefully healthy credential.
		s.pool.Unstick(auth.NormalizeProvider(a.Provider), clientToken, slotID)

		if retryable {
			// Withhold the response and signal the caller to switch credentials.
			// The attempt-only row is excluded from normal dashboard aggregates,
			// but preserves per-account 401/403/429 and hard-failure evidence for
			// the seven-day experiment.
			log.Warnf("proxy: %s returned %d — retrying on another credential. body=%s", a.ID, resp.StatusCode, truncate(errBody, 500))
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.NormalizeProvider(a.Provider),
				AuthID: a.ID, AuthLabel: a.Label, AuthKind: authKind, Model: model, Status: resp.StatusCode,
				Stream: stream, Path: path, Attempts: attempts, DurationMs: time.Since(start).Milliseconds(),
				Error:       fmt.Sprintf("upstream %d (credential attempt withheld)", resp.StatusCode),
				AttemptOnly: true, ClaudeAudit: claudeAudit,
			})
			return true, false, &deferredResponse{
				status: resp.StatusCode, header: resp.Header.Clone(), body: errBody,
				authID: a.ID, authLabel: a.Label, authKind: authKind, claudeAudit: claudeAudit,
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
			ClaudeAudit: claudeAudit,
		})

		copySafeRetryHeaders(c, resp.Header)
		writeAPIError(c, auth.NormalizeProvider(a.Provider), publicUpstreamError(resp.StatusCode, errBody))
		return false, true, nil
	}

recoveredFromSignature:
	// Success or non-retryable error — stream response body to client.
	authKind := "oauth"
	if a.Kind == auth.KindAPIKey {
		authKind = "apikey"
	}
	claudeAudit := claudeIdentityAudit(prepared, accountKey)

	var counts usage.Counts
	var sub advisor.SubUsage
	counts.Requests = 1

	// When this credential rewrote the request's model name (relay vendors
	// with vendor-prefixed names), rewrite it back in the response so the
	// client keeps seeing the model it asked for. Claude Code uses the
	// model field on message_start to correlate conversation turns; a
	// vendor-prefixed name breaks multi-turn continuation.
	var rewriteClientModel string
	terminalError := ""
	if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		rewriteClientModel = model
	}

	if stream && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		// Headers commit lazily on the first byte, so a stream that breaks
		// before any output reaches the client can transparently fail over to
		// another credential (the common "RST right after 200" case).
		res := streamSSE(c, resp, &counts, &sub, rewriteClientModel, func() { writeResponseHeaders(c, resp) })
		if !res.sawTerminal && !res.wroteAny {
			_ = resp.Body.Close()
			if isClientDisconnect(c.Request.Context(), res.err) {
				a.MarkClientCancel(errString(res.err))
				s.emitLog(requestlog.Record{
					Client: clientName, ClientToken: maskClientToken(clientToken),
					Provider: auth.NormalizeProvider(a.Provider), AuthID: a.ID, AuthLabel: a.Label,
					AuthKind: authKind, Model: model, Status: 499, Stream: stream, Path: path, Attempts: attempts,
					DurationMs: time.Since(start).Milliseconds(), Error: "client canceled before first event", ClaudeAudit: claudeAudit,
				})
				return false, true, nil
			}
			log.Warnf("proxy: stream broke before any output via %s (attempt %d, %s): %v — retrying on another credential",
				a.ID, attempts, time.Since(start).Round(time.Millisecond), res.err)
			a.MarkFailure("stream broke before first event: " + errString(res.err))
			s.emitLog(requestlog.Record{
				Client: clientName, ClientToken: maskClientToken(clientToken), Provider: auth.NormalizeProvider(a.Provider),
				AuthID: a.ID, AuthLabel: a.Label, AuthKind: authKind, Model: model, Status: http.StatusBadGateway,
				Stream: stream, Path: path, Attempts: attempts, DurationMs: time.Since(start).Milliseconds(),
				Error: "stream broke before first event", AttemptOnly: true, ClaudeAudit: claudeAudit,
			})
			return true, false, &deferredResponse{
				status: http.StatusBadGateway, authID: a.ID, authLabel: a.Label, authKind: authKind, claudeAudit: claudeAudit,
			}
		}
		a.MarkSuccess()
		if !res.sawTerminal {
			terminalError = "stream truncated before terminal event"
			log.Warnf("proxy: SSE truncated mid-stream via %s (attempt %d, events=%d, bytes=%d, %s): %v",
				a.ID, attempts, res.events, res.bytes, time.Since(start).Round(time.Millisecond), res.err)
		}
	} else {
		writeResponseHeaders(c, resp)
		a.MarkSuccess()
		respBody, _ := io.ReadAll(resp.Body)
		if rewriteClientModel != "" {
			respBody = rewriteResponseModel(respBody, rewriteClientModel)
		}
		_, _ = c.Writer.Write(respBody)
		counts.Add(extractUsageFromJSON(respBody, &sub))
	}
	_ = resp.Body.Close()
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
				multiplier = s.saas.MultiplierFor(info, auth.NormalizeProvider(a.Provider))
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
		for _, sc := range sub.Snapshot() {
			clientCounts.Add(sc)
		}
		s.usage.RecordClient(clientToken, clientName, clientCounts, costUSD+advisorCost)
	}
	if claudeAudit != nil {
		log.Infof(
			"claude-identity-audit: account=%s class=%s mode=%s identity_mapped=%t",
			claudeAudit.AccountHash,
			claudeAudit.RequestClass,
			claudeAudit.IdentityMode,
			claudeAudit.AccountIdentityMapped,
		)
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
		Error:       terminalError,
		UserID:      userID,
		Multiplier:  multiplier,
		ClaudeAudit: claudeAudit,
	})
	return false, true, nil
}

// doForwardAnthropicAPIKey is the API-key passthrough for Anthropic-shaped
// upstreams (api.anthropic.com or third-party relays). Unlike the OAuth path,
// we inject no Claude Code mimicry headers and do not use uTLS: the request
// is forwarded essentially verbatim. The only request-side change allowed is
// the per-credential model rewrite (and the matching response-side rewrite)
// so model_map'd relay vendors keep working.
//
// Failure handling is driven by classifyUpstreamStatus, which separates
// faults the client caused from faults the upstream or the credential caused
// (see upstream_health.go):
//
//	faultNone       → MarkSuccess, response forwarded
//	faultCredential → MarkHardFailure, withheld + retried on another credential
//	faultUpstream   → MarkFailure,     withheld + retried on another credential
//	faultClient     → no health change, forwarded verbatim, never retried
//
// Only the upstream-side classes touch health, so one client sending
// malformed requests can no longer degrade a channel that is serving
// everyone else correctly.
//
// Both upstream-side classes feed cc-core's API-key circuit breaker: enough
// consecutive faults pause the channel for a self-expiring, exponentially
// growing interval, so traffic rotates onto another key instead of re-paying
// a doomed round-trip per request, and the channel probes itself back into
// rotation with no operator involvement. A definitive credential rejection
// pauses on the first strike. Neither ever *retires* the channel — only the
// explicit Disabled flag takes an API key offline for good.
//
// A <400 response is additionally checked against the Messages API wire
// format before it is committed or billed (validateAnthropicResponse) — a
// relay answering 200 with an HTML block page is a faultUpstream, not a
// zero-token success.
//
// The (retry, done, deferred) contract matches doForward: a retryable fault
// is withheld and returned in deferred so forwardWithFailover can switch
// credentials transparently, replaying the withheld response only if every
// credential is exhausted. There is no bootstrap-wait gate on this path, so
// it takes no isRetry flag.
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
		writeAPIError(c, auth.NormalizeProvider(a.Provider), APIError{Status: http.StatusInternalServerError, Code: "request_preparation_failed", Message: "The request could not be prepared. Please try again."})
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
		writeAPIError(c, auth.NormalizeProvider(a.Provider), APIError{Status: http.StatusBadGateway, Code: "service_connection_error", Message: "The model service could not be reached. Please try again."})
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
	ccstream.Decompress(resp)

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
						ccstream.Decompress(rresp)
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

	// Response-contract check. A <400 status is not on its own evidence that
	// the exchange worked: a dead relay in front of the real API answers 200
	// with an HTML block page, which would otherwise be marked healthy,
	// streamed to the client as an empty response, and billed as zero tokens.
	// Validate the wire format first and demote a violation to faultUpstream
	// so it is withheld and retried like any other upstream failure.
	//
	// Buffer the body so the peek is non-consuming; Close still reaches the
	// original body, so the connection is released exactly as before.
	statusForFault := resp.StatusCode
	var bodyBuf *bufio.Reader
	if resp.StatusCode < 400 {
		bodyBuf = bufio.NewReaderSize(resp.Body, 64*1024)
		resp.Body = struct {
			io.Reader
			io.Closer
		}{bodyBuf, resp.Body}
		if v := validateAnthropicResponse(resp.Header, bodyBuf); v.Detail != "" {
			log.Warnf("proxy(apikey): %s returned %d but the body is not an Anthropic response (%s) — treating as an upstream failure",
				a.ID, resp.StatusCode, truncate([]byte(v.Detail), 300))
			statusForFault = http.StatusBadGateway
		}
	}

	// Credential health bookkeeping + retryability, computed before writing
	// anything so a retryable fault can be withheld and retried on another
	// credential while the pool still has a slot.
	fault := classifyUpstreamStatus(statusForFault)
	switch fault {
	case faultNone:
		a.MarkSuccess()
	case faultCredential:
		// Revoked, forbidden, or out of funds — definitive, so cc-core pauses
		// the channel on this single strike rather than re-presenting a dead
		// key on every subsequent request. Still never sticky for an API key:
		// the pause expires by itself.
		a.MarkHardFailure(fmt.Sprintf("upstream %d", statusForFault))
	case faultUpstream:
		// Throttling, gateway errors, or a contract violation. Not a verdict
		// on the key itself, so it takes several in a row before cc-core
		// pauses the channel — enough to ride out the ordinary weather of a
		// shared relay without pausing anything that still works.
		a.MarkFailure(fmt.Sprintf("upstream %d", statusForFault))
	case faultClient:
		// The caller's own request is at fault (400 malformed, 404 route not
		// implemented by this relay, 413 too large, …). Another credential
		// would return the identical error, so forward it through untouched
		// and leave credential health alone.
	}

	if fault.retryable() {
		errBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		log.Warnf("proxy(apikey): %s returned %d — retrying on another credential. body=%s", a.ID, resp.StatusCode, truncate(errBody, 500))
		// statusForFault, not resp.StatusCode: a contract violation arrives as
		// 200 but must be replayed to the client as the 502 it really is if
		// every credential ends up exhausted.
		return true, false, &deferredResponse{
			status: statusForFault,
			header: resp.Header.Clone(),
			body:   errBody,
			authID: a.ID,
		}
	}

	var counts usage.Counts
	var sub advisor.SubUsage
	var errSnippet string
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		errSnippet = truncate(errBody, 500)
		log.Warnf("proxy(apikey): %s returned %d — body=%s", a.ID, resp.StatusCode, errSnippet)
		copySafeRetryHeaders(c, resp.Header)
		writeAPIError(c, auth.NormalizeProvider(a.Provider), publicUpstreamError(resp.StatusCode, errBody))
	} else {
		writeResponseHeaders(c, resp)
		counts.Requests = 1
		// Dispatch on the client's stream flag + the actual bytes, not the
		// upstream Content-Type alone: relays are known to stream the SSE
		// back as text/plain, which under a header-only check fell through to
		// the whole-body JSON parse and silently lost all usage (billing =
		// $0). Same fix the Codex path already carries. Peek is non-consuming.
		if stream && responseIsSSE(resp.Header, bodyBuf) {
			// Headers are already committed above (the 4xx branch needs them),
			// so commit=nil — no cross-credential retry on this verbatim
			// passthrough path. The relay still adds keepalive + truncation
			// detection so a broken stream is logged, not silently swallowed.
			res := streamSSE(c, resp, &counts, &sub, rewriteClientModel, nil)
			if !res.sawTerminal && !isClientDisconnect(c.Request.Context(), res.err) {
				log.Warnf("proxy(apikey): SSE truncated mid-stream via %s (events=%d, bytes=%d, %s): %v",
					a.ID, res.events, res.bytes, time.Since(start).Round(time.Millisecond), res.err)
			}
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
					multiplier = s.saas.MultiplierFor(info, auth.NormalizeProvider(a.Provider))
				}
			}
		}
		advisorCost := s.recordSubUsage(c, a, "apikey", clientToken, clientName, model, path, resp.StatusCode, sub)
		if counts.Requests > 0 && clientToken != "" {
			var clientCounts usage.Counts
			clientCounts.Add(counts)
			for _, sc := range sub.Snapshot() {
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

// applyAnthropicHeaders is a thin adapter from this fork's *auth.Auth to
// cc-core/mimicry.ApplyClaudeCodeHeaders. The full header policy — pinned
// User-Agent / X-Stainless-* / Anthropic-Beta (OAuth vs API-key list) /
// X-Claude-Code-Session-Id / x-client-request-id / Accept-Encoding — lives in
// cc-core/mimicry so hypitoken and CPA-Claude stay byte-identical against the
// pinned Claude Code version target. Bumping the fingerprint is a cc-core +
// dependency-bump, not a per-fork edit.
func applyAnthropicHeaders(req *http.Request, a *auth.Auth, stream, isAnthropicBase bool, id mimicry.SimIdentity, body []byte) {
	token, kind := a.Credentials()
	mimicry.ApplyClaudeCodeHeaders(req, token, kindToMimicry(kind), stream, isAnthropicBase, id, body)
}

func applyAnthropicPreparedHeaders(req *http.Request, a *auth.Auth, stream, isAnthropicBase bool, prepared mimicry.BodyTransformResult) error {
	token, kind := a.Credentials()
	return mimicry.ApplyClaudeCodePreparedRequest(req, token, a.AccountKey(), kindToMimicry(kind), stream, isAnthropicBase, prepared)
}

func genuineModeForAuth(a *auth.Auth) (mimicry.GenuineRequestMode, error) {
	mode := a.ClaudeIdentityModeValue()
	switch mode {
	case auth.ClaudeIdentityModePreserve:
		return mimicry.GenuineRequestPreserve, nil
	case auth.ClaudeIdentityModeRewrite:
		return mimicry.GenuineRequestRewrite, nil
	default:
		return mimicry.GenuineRequestModeUnspecified, fmt.Errorf("unsupported account mode %q", mode)
	}
}

func prepareClaudeRewriteBody(body []byte, model string, a *auth.Auth, id mimicry.SimIdentity, policy mimicry.RequestPolicy) (mimicry.BodyTransformResult, error) {
	working := body
	// Any permitted body edits happen before the atomic transform. The rewrite
	// subsequently validates the prepared body, so these exact-byte edits cannot
	// leave a partially rewritten request behind.
	if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		rewritten, err := rewriteModelFieldPreservingBytes(working, upstreamModel)
		if err != nil {
			return mimicry.BodyTransformResult{}, fmt.Errorf("model rewrite (%s -> %s): %w", model, upstreamModel, err)
		}
		working = rewritten
	}
	if normalized, changed := mimicry.NormalizeDateline(working); changed {
		working = normalized
	}
	return mimicry.PrepareClaudeCodeRequest(working, model, id, policy, kindToMimicry(a.Kind))
}

func kindToMimicry(k auth.Kind) string {
	if k == auth.KindAPIKey {
		return mimicry.KindAPIKey
	}
	return mimicry.KindOAuth
}

// streamSSE copies SSE events to the client as they arrive and parses
// message_delta events to accumulate usage. When rewriteClientModel is
// non-empty, each data: JSON has its top-level "model" and nested
// "message.model" fields rewritten to that value before being forwarded.
//
// Framing uses cc-core/stream.SSEScanner so the event/data parsing logic
// is shared with other forks; this function is the proxy-specific glue
// (model rewrite + usage accumulation + flusher dispatch).
//
// Resilience (mirrors the Codex relay): headers commit lazily via commit() on
// the first downstream byte, so a stream that breaks before any output can be
// retried by the caller on another credential; a synthetic Anthropic `ping`
// event is emitted after >=10s of downstream silence so intermediaries
// (Cloudflare Tunnel / the client idle timeout) don't cut a long stream while
// the model is mid-think or running a server-side advisor sub-call; and the
// terminal event (message_stop) is tracked so a truncated upstream is reported
// instead of looking like a clean end-of-stream.
func streamSSE(c *gin.Context, resp *http.Response, counts *usage.Counts, sub *advisor.SubUsage, rewriteClientModel string, commit func()) sseRelayResult {
	flusher, _ := c.Writer.(http.Flusher)
	sc := ccstream.NewSSEScanner(resp.Body, 64*1024)
	events := 0

	// next supplies framing + model-rewrite + usage to the shared relay; the
	// relay (cc-core/stream.Relay) owns keepalive + lazy commit + write locking.
	next := func() (out []byte, terminal bool, err error) {
		if !sc.Scan() {
			if e := sc.Err(); e != nil {
				return nil, false, e
			}
			return nil, false, io.EOF
		}
		line := sc.Line()
		outLine := line
		if payload := sc.Data(); payload != nil {
			if rewriteClientModel != "" && len(payload) > 0 && payload[0] == '{' {
				if rewritten := rewriteResponseModel(payload, rewriteClientModel); rewritten != nil {
					// Preserve the original line's trailing newline style.
					trim := bytes.TrimRight(line, "\r\n")
					tail := line[len(trim):]
					rebuilt := make([]byte, 0, len("data: ")+len(rewritten)+len(tail))
					rebuilt = append(rebuilt, []byte("data: ")...)
					rebuilt = append(rebuilt, rewritten...)
					rebuilt = append(rebuilt, tail...)
					outLine = rebuilt
				}
			}
			switch sc.Event() {
			case "message_start", "message_delta":
				mergeSSEUsage(counts, sub, payload)
				events++
			case "message_stop", "error":
				terminal = true
			}
		}
		return outLine, terminal, nil
	}

	// A synthetic `ping` event is exactly what the real Anthropic API sends
	// during gaps, so Claude Code handles it natively.
	r := ccstream.Relay(c.Writer, func() {
		if flusher != nil {
			flusher.Flush()
		}
	}, ccstream.RelayOptions{
		Commit:           commit,
		KeepaliveIdle:    10 * time.Second,
		KeepalivePayload: []byte("event: ping\ndata: {\"type\": \"ping\"}\n\n"),
		Next:             next,
	})
	return sseRelayResult{sawTerminal: r.SawTerminal, wroteAny: r.WroteAny, events: events, bytes: r.Bytes, err: r.Err}
}

// sseRelayResult reports the outcome of an Anthropic SSE relay so the caller can
// choose between a transparent retry (nothing reached the client yet) and a
// logged give-up (bytes already committed downstream — uninterruptible).
type sseRelayResult struct {
	sawTerminal bool  // message_stop / error event relayed
	wroteAny    bool  // at least one byte was committed to the client
	events      int   // message_start/_delta events relayed (diagnostics)
	bytes       int64 // bytes written downstream (diagnostics)
	err         error // underlying read error when the stream broke early
}

// errString renders an error for a log/record field, tolerating nil.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type usageJSON struct {
	InputTokens              int64                    `json:"input_tokens"`
	OutputTokens             int64                    `json:"output_tokens"`
	CacheCreationInputTokens int64                    `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64                    `json:"cache_read_input_tokens"`
	Iterations               []advisor.IterationUsage `json:"iterations,omitempty"`
}

func (u usageJSON) toCounts() usage.Counts {
	return usage.Counts{
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CacheCreateTokens: u.CacheCreationInputTokens,
		CacheReadTokens:   u.CacheReadInputTokens,
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
func (s *Server) recordSubUsage(c *gin.Context, a *auth.Auth, authKind, clientToken, clientName, _ string, path string, status int, sub advisor.SubUsage) float64 {
	if status >= 400 || sub.IsEmpty() {
		return 0
	}
	provider := auth.NormalizeProvider(a.Provider)
	var total float64
	info, hasSaaS := saasInfoFrom(c)
	var subUserID int64
	var subMultiplier float64
	if hasSaaS && s.saas != nil {
		subUserID = info.UserID
		subMultiplier = s.saas.MultiplierFor(info, provider)
	}
	for subModel, sc := range sub.Snapshot() {
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
func extractUsageFromJSON(body []byte, sub *advisor.SubUsage) usage.Counts {
	var wrap struct {
		Usage usageJSON `json:"usage"`
	}
	_ = json.Unmarshal(body, &wrap)
	if sub != nil {
		sub.ReplaceFrom(wrap.Usage.Iterations)
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
func mergeSSEUsage(dst *usage.Counts, sub *advisor.SubUsage, payload []byte) {
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
		sub.ReplaceFrom(u.Iterations)
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

// parseUnifiedRatelimitRejected inspects Anthropic's `anthropic-ratelimit-
// unified-*` headers and classifies a quota rejection.
//
// Real responses carry a snapshot like:
//
//	anthropic-ratelimit-unified-status: rejected           ← top-level decision
//	anthropic-ratelimit-unified-5h-status: allowed         ← shared 5h window
//	anthropic-ratelimit-unified-7d-status: allowed         ← shared 7d window
//	anthropic-ratelimit-unified-5h-reset / -7d-reset / -reset
//	anthropic-ratelimit-unified-representative-claim: five_hour
//
// The shared 5h/7d buckets are account-wide — every model draws on them. There
// is NO per-model bucket header: a model-scoped window (e.g. fable's weekly
// allotment, ~50% of weekly per unified-fallback-percentage) surfaces only in
// the oauth/usage `limits[]` body, never in headers. So a scoped rejection is
// inferred from the request MODEL — when the top level is rejected but neither
// shared bucket is, and the failing request is for a model with its own scope
// (fable), the cooldown is scoped to that family instead of the whole account.
//
// reqModel is the client-requested model driving this request.
//
// Returns:
//   - ok=false: not rejected.
//   - scope != "": model-family-scoped cooldown (never banned) — caller must
//     use MarkModelRateLimited so the credential keeps serving other models.
//   - scope == "", banned=false: account-wide cooldown until resetAt.
//   - scope == "", banned=true: rejected with no usable FUTURE reset stamp —
//     the stealth-ban signature (a banned account stays "rejected" forever with
//     no recovery time). Caller escalates.
func parseUnifiedRatelimitRejected(h http.Header, reqModel string) (resetAt time.Time, scope string, banned bool, ok bool) {
	const statusPrefix = "rejected"
	isRejected := func(headerName string) bool {
		v := strings.ToLower(strings.TrimSpace(h.Get(headerName)))
		return v != "" && strings.HasPrefix(v, statusPrefix)
	}

	// Shared 5h/7d buckets — a rejection here is account-wide.
	sharedBuckets := []struct{ statusHdr, resetHdr string }{
		{"Anthropic-Ratelimit-Unified-5h-Status", "Anthropic-Ratelimit-Unified-5h-Reset"},
		{"Anthropic-Ratelimit-Unified-7d-Status", "Anthropic-Ratelimit-Unified-7d-Reset"},
	}
	sharedRejected := false
	var sharedReset time.Time
	for _, b := range sharedBuckets {
		if !isRejected(b.statusHdr) {
			continue
		}
		sharedRejected = true
		if t, parsed := parseUnixSecondsHeader(h.Get(b.resetHdr)); parsed && t.After(sharedReset) {
			sharedReset = t
		}
	}

	topRejected := isRejected("Anthropic-Ratelimit-Unified-Status")
	if !sharedRejected && !topRejected {
		return time.Time{}, "", false, false
	}

	now := time.Now()
	topReset, topOK := parseUnixSecondsHeader(h.Get("Anthropic-Ratelimit-Unified-Reset"))

	// Model-scoped rejection: top-level rejected, shared buckets fine, and the
	// request is for a model with its own quota scope. Cool down only that model
	// family — never ban the whole credential. Fall back to a modest re-probe
	// window if no reset stamp is present.
	if !sharedRejected {
		if s := auth.AnthropicModelScope(reqModel); s != "" {
			if topOK && topReset.After(now) {
				resetAt = clampReset(topReset)
			} else {
				resetAt = now.Add(time.Hour)
			}
			return resetAt, s, false, true
		}
	}

	// Account-wide. Prefer the top-level reset, else the latest shared bucket
	// reset — a 7d rejection isn't released by the (sooner) 5h reset.
	if topOK && topReset.After(now) {
		return clampReset(topReset), "", false, true
	}
	if !sharedReset.IsZero() && sharedReset.After(now) {
		return clampReset(sharedReset), "", false, true
	}
	// Rejected with no future reset — the stealth-ban signature.
	return time.Time{}, "", true, true
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

// isLongContextRejection reports whether a 429 body is the per-request
// "this prompt is too long for your subscription's context window" rejection
// rather than a credential-level quota/rate problem. These fire when a request
// exceeds the standard 200K context and would need usage-based billing
// ("extra usage"/credits) that subscription accounts don't have — so EVERY
// credential rejects the identical request. It must not cool down the
// credential or trigger a cross-pool retry (which would flag the whole pool
// unavailable for one oversized request). Anthropic has shipped the message
// under at least two wordings, hence the multi-marker match. Case-insensitive.
func isLongContextRejection(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	lower := bytes.ToLower(b)
	markers := [][]byte{
		[]byte("extra usage is required"),                     // older wording
		[]byte("usage credits are required for long context"), // current wording
		[]byte("long context request"),                        // defensive: copy tweaks
	}
	for _, m := range markers {
		if bytes.Contains(lower, m) {
			return true
		}
	}
	return false
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

// rewriteModelField is the legacy best-effort model mapper. It intentionally
// retains the pre-policy map decode/remarshal behavior because Preserve is the
// experiment's behavior-for-behavior control path.
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

// rewriteModelFieldPreservingBytes is restricted to rewrite. It changes
// only the top-level model value so the subsequent identity rewrite can verify
// and atomically install the resulting genuine Claude Code request.
func rewriteModelFieldPreservingBytes(body []byte, upstream string) ([]byte, error) {
	span, err := requireTopLevelJSONFieldSpan(body, "model")
	if err != nil {
		return nil, err
	}
	var current string
	if err = json.Unmarshal(body[span.start:span.end], &current); err != nil {
		return nil, errors.New("top-level model is not a JSON string")
	}
	mb, err := json.Marshal(upstream)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(body)-span.end+span.start+len(mb))
	out = append(out, body[:span.start]...)
	out = append(out, mb...)
	out = append(out, body[span.end:]...)
	return out, nil
}

type jsonFieldSpan struct {
	start int
	end   int
}

func requireTopLevelJSONFieldSpan(body []byte, name string) (jsonFieldSpan, error) {
	if !json.Valid(body) {
		return jsonFieldSpan{}, errors.New("request body is not valid JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	opening, err := dec.Token()
	if err != nil || opening != json.Delim('{') {
		return jsonFieldSpan{}, errors.New("request body is not a JSON object")
	}

	var found jsonFieldSpan
	count := 0
	for dec.More() {
		keyToken, tokenErr := dec.Token()
		if tokenErr != nil {
			return jsonFieldSpan{}, tokenErr
		}
		key, ok := keyToken.(string)
		if !ok {
			return jsonFieldSpan{}, errors.New("JSON object key is not a string")
		}

		before := int(dec.InputOffset())
		var raw json.RawMessage
		if decodeErr := dec.Decode(&raw); decodeErr != nil {
			return jsonFieldSpan{}, decodeErr
		}
		after := int(dec.InputOffset())
		if before < 0 || after < before || after > len(body) || len(raw) == 0 {
			return jsonFieldSpan{}, errors.New("invalid JSON decoder offsets")
		}
		relativeStart := bytes.LastIndex(body[before:after], raw)
		if relativeStart < 0 {
			return jsonFieldSpan{}, errors.New("could not locate raw JSON value")
		}
		if key == name {
			count++
			start := before + relativeStart
			found = jsonFieldSpan{start: start, end: start + len(raw)}
		}
	}
	if _, err = dec.Token(); err != nil {
		return jsonFieldSpan{}, err
	}
	if count == 0 {
		return jsonFieldSpan{}, fmt.Errorf("JSON object has no %q field", name)
	}
	if count != 1 {
		return jsonFieldSpan{}, fmt.Errorf("JSON object has duplicate %q fields", name)
	}
	return found, nil
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
