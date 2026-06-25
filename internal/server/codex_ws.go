package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	gorillaws "github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/codexws"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/usage"
)

// Codex WebSocket ingress. Real codex-tui 0.135.0 streams a turn over a
// WebSocket; a long-lived WS carries protocol-level ping/pong, so it survives
// the silent gaps (reasoning -> answer, tool thinking) that truncate the HTTP
// SSE path and surface to clients as "stream disconnected before completion".
//
// This is a passthrough relay: the client already speaks the Codex WS protocol,
// so frames are forwarded verbatim between client and upstream. We only parse
// out, best-effort: the model (for credential acquisition + billing),
// previous_response_id (for the cross-group safety boundary, see codex_session.go),
// the response id (to bind a conversation to its account), and usage (for
// billing, carried inside the terminal event). The whole path is opt-in
// (config.codex_ws.enabled) and unverified against a real ChatGPT token — see
// CLAUDE.md's Codex-OAuth caveat.

const (
	codexWSFirstFrameTimeout = 30 * time.Second
	codexWSUpstreamPingEvery = 20 * time.Second
	codexWSReadDeadline      = 15 * time.Minute
	codexWSWriteDeadline     = 2 * time.Minute
	// codexWSMaxAcquire bounds dial-time credential switches. Once the first
	// upstream frame is relayed to the client the credential is locked (no
	// silent switch is possible after bytes are committed to the client).
	codexWSMaxAcquire = 4
)

var codexWSUpgrader = gorillaws.Upgrader{
	ReadBufferSize:    4096,
	WriteBufferSize:   4096,
	EnableCompression: true,
	// The bearer token already authenticated the request (clientAuth middleware
	// ran before this handler); the WS Origin header is not a security boundary
	// for a token-authenticated API, so accept any origin.
	CheckOrigin: func(*http.Request) bool { return true },
}

func isCodexWSUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// handleCodexResponsesWS upgrades a /v1/responses GET into a WebSocket and
// bridges it to the ChatGPT Codex backend over an upstream WebSocket dialed with
// the cc-core uTLS fingerprint.
func (s *Server) handleCodexResponsesWS(c *gin.Context) {
	if !isCodexWSUpgrade(c.Request) {
		c.AbortWithStatusJSON(http.StatusUpgradeRequired, gin.H{"error": "WebSocket upgrade required (Upgrade: websocket)"})
		return
	}
	const provider = auth.ProviderOpenAI
	start := time.Now()

	clientTokV, _ := c.Get("client_token")
	clientToken, _ := clientTokV.(string)
	if clientToken == "" {
		clientToken = c.ClientIP()
	}
	clientNameV, _ := c.Get("client_name")
	clientName, _ := clientNameV.(string)

	// Pre-flight gates — same single funnel as forward(): SaaS balance/caps,
	// then per-(provider|token) RPM and concurrency. These can still answer with
	// an HTTP status because the WS handshake has not happened yet.
	saasInfo, saasOK := saasInfoFrom(c)
	var clientGroup string
	if saasOK && s.saas != nil {
		clientGroup = s.saas.CredentialGroup(saasInfo)
		if pre := s.saas.PreCheck(c.Request.Context(), saasInfo); pre != nil {
			c.AbortWithStatusJSON(pre.Status, pre.Body)
			return
		}
	} else if entry, ok := s.tokens.Lookup(clientToken); ok {
		clientGroup = entry.Group
	}

	rpmKey := auth.NormalizeProvider(provider) + "|" + clientToken
	if limit := s.clientRPM(c, provider, clientToken); limit > 0 {
		if ok, retry := s.rpm.Allow(rpmKey, limit); !ok {
			c.Header("Retry-After", strconv.Itoa(retry))
			c.AbortWithStatusJSON(429, gin.H{"error": "rate limit exceeded", "retry_after": retry})
			return
		}
	}
	if maxConc := s.clientMaxConcurrent(c, provider, clientToken); maxConc > 0 {
		inflightKey := auth.NormalizeProvider(provider) + "|" + clientToken
		cur, releaseSlot := s.inflight.Begin(inflightKey)
		defer releaseSlot()
		if int(cur) > maxConc {
			c.Header("Retry-After", "5")
			c.AbortWithStatusJSON(429, gin.H{"error": "too many concurrent requests", "max_concurrent": maxConc})
			return
		}
	}

	slotID := clientSlotID(c)

	// Upgrade the client connection. Past this point no HTTP status can be sent;
	// failures close the WS with a control frame.
	clientConn, err := codexWSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Warnf("codex ws: client upgrade failed: %v", err)
		return
	}
	defer clientConn.Close()
	clientConn.SetReadLimit(s.cfg.CodexWS.ReadLimitBytes)

	// First client frame (response.create) — learn model + previous_response_id
	// before acquiring a credential.
	_ = clientConn.SetReadDeadline(time.Now().Add(codexWSFirstFrameTimeout))
	mt, firstFrame, err := clientConn.ReadMessage()
	if err != nil || mt != gorillaws.TextMessage {
		closeCodexWS(clientConn, gorillaws.CloseProtocolError, "expected initial JSON frame")
		return
	}
	_ = clientConn.SetReadDeadline(time.Time{})

	model := codexWSExtractModel(firstFrame)
	if model == "" {
		model = "unknown"
	}

	// Cross-group previous_response_id safety: if the chain belongs to this
	// group's sticky account, keep it; otherwise strip it so the upstream
	// rebuilds from full input (prevents replaying tenant A's chain on B).
	if prevID := codexPreviousResponseID(firstFrame); prevID != "" {
		if _, ok := s.codexRespAccount.Get(clientGroup, prevID); !ok {
			firstFrame = removeCodexPreviousResponseID(firstFrame)
			log.Infof("codex ws: stripped cross-group previous_response_id (group=%q)", clientGroup)
		}
	}

	betaValue := codexws.CodexOpenAIBetaWS
	if s.cfg.CodexWS.BetaVersion == "v1" {
		betaValue = codexws.CodexOpenAIBetaWSV1
	}
	wsURL := codexWSUpstreamURL(s.cfg.ChatGPTBackendBaseURL)

	// Acquire an OAuth credential, retrying dial-time failures on another one.
	tried := map[string]bool{}
	var up codexws.Conn
	var a *auth.Auth
	for i := 0; i < codexWSMaxAcquire; i++ {
		exclude := make([]string, 0, len(tried))
		for id := range tried {
			exclude = append(exclude, id)
		}
		cand := s.pool.Acquire(c.Request.Context(), provider, clientToken, clientGroup, model, slotID, exclude...)
		if cand == nil {
			break
		}
		tried[cand.ID] = true
		if cand.Kind != auth.KindOAuth {
			// API-key relays can't speak the ChatGPT WS backend.
			s.pool.Release(provider, clientToken, slotID)
			continue
		}
		snap := cand.Snapshot()
		accessToken, _ := cand.Credentials()
		accountID, _ := cand.CodexIdentity()
		header := codexws.BuildUpstreamHeaders(accessToken, accountID, slotID, betaValue)
		conn, resp, derr := codexws.Dial(c.Request.Context(), codexws.DialConfig{
			URL:       wsURL,
			Header:    header,
			ProxyURL:  snap.ProxyURL,
			UseUTLS:   s.cfg.UseUTLS,
			ReadLimit: s.cfg.CodexWS.ReadLimitBytes,
		})
		// On a non-101 the body carries the upstream error; on success gorilla
		// hands back a NopCloser over leftover bytes (the live conn lives on
		// `conn`, not resp.Body), so closing here is safe either way. Headers
		// stay readable after the body is closed.
		var status int
		var retryAfter time.Time
		if resp != nil {
			status = resp.StatusCode
			retryAfter = parseRetryAfter(resp.Header)
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
		}
		if derr != nil {
			log.Warnf("codex ws: upstream dial via %s failed (status=%d): %v", cand.ID, status, derr)
			switch status {
			case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
				s.pool.ReportUpstreamError(cand, status, retryAfter)
				s.pool.Unstick(provider, clientToken, slotID)
			default:
				cand.MarkFailure(derr.Error())
			}
			s.pool.Release(provider, clientToken, slotID)
			continue
		}
		a = cand
		up = conn
		break
	}
	if up == nil || a == nil {
		closeCodexWS(clientConn, gorillaws.CloseTryAgainLater, "no upstream credential available")
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken), Provider: provider, Model: model,
			Stream: true, Path: "/v1/responses", Status: 503, DurationMs: time.Since(start).Milliseconds(),
			Error: "ws: no upstream credential",
		})
		return
	}
	defer up.Close()
	defer s.pool.Release(provider, clientToken, slotID)

	// Relay the first frame upstream, then run the bidirectional pump.
	_ = up.SetWriteDeadline(time.Now().Add(codexWSWriteDeadline))
	if err := up.WriteMessage(codexws.TextMessage, firstFrame); err != nil {
		log.Warnf("codex ws: first upstream write via %s failed: %v", a.ID, err)
		closeCodexWS(clientConn, gorillaws.CloseInternalServerErr, "upstream write failed")
		return
	}

	var counts usage.Counts
	s.pumpCodexWS(c.Request.Context(), clientConn, up, a, clientGroup, &counts)

	s.billCodexWS(c, a, model, clientToken, clientName, &counts, start)
	if counts.Requests > 0 {
		a.MarkSuccess()
	}
}

// pumpCodexWS relays frames between the client and upstream WebSockets until
// either side closes. Usage and response-id binding are extracted from the
// upstream->client direction; the cross-group previous_response_id rewrite is
// applied on the client->upstream direction for follow-up turns. Both relay
// goroutines are joined before returning so counts is safe for billing.
func (s *Server) pumpCodexWS(ctx context.Context, client *gorillaws.Conn, up codexws.Conn, a *auth.Auth, group string, counts *usage.Counts) {
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			_ = up.Close()
			_ = client.Close()
		})
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// upstream -> client
	go func() {
		defer wg.Done()
		defer stop()
		for {
			_ = up.SetReadDeadline(time.Now().Add(codexWSReadDeadline))
			mt, data, err := up.ReadMessage()
			if err != nil {
				return
			}
			if mt == codexws.TextMessage && len(data) > 0 {
				if rid := codexResponseID(data); rid != "" {
					s.codexRespAccount.Bind(group, rid, a.ID)
				}
				counts.Add(extractCodexBackendUsageFromJSON(data))
				if codexTerminalEvent(data) {
					counts.Requests++
				}
			}
			_ = client.SetWriteDeadline(time.Now().Add(codexWSWriteDeadline))
			if err := client.WriteMessage(gorillaws.TextMessage, data); err != nil {
				return
			}
		}
	}()

	// client -> upstream
	go func() {
		defer wg.Done()
		defer stop()
		for {
			mt, data, err := client.ReadMessage()
			if err != nil {
				return
			}
			if mt == gorillaws.TextMessage {
				if prevID := codexPreviousResponseID(data); prevID != "" {
					if _, ok := s.codexRespAccount.Get(group, prevID); !ok {
						data = removeCodexPreviousResponseID(data)
					}
				}
			}
			_ = up.SetWriteDeadline(time.Now().Add(codexWSWriteDeadline))
			if err := up.WriteMessage(codexws.TextMessage, data); err != nil {
				return
			}
		}
	}()

	// Upstream keepalive ping during quiet turns.
	go func() {
		t := time.NewTicker(codexWSUpstreamPingEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				stop()
				return
			case <-t.C:
				_ = up.Ping(time.Now().Add(5 * time.Second))
			}
		}
	}()

	wg.Wait()
}

// billCodexWS runs the same billing funnel as the HTTP Codex path: official cost
// -> SaaS Charge (group×provider multiplier) -> usage ledger -> request log. A
// multi-turn WS connection accumulates one Requests increment per terminal
// event, so counts already reflects every billed turn.
func (s *Server) billCodexWS(c *gin.Context, a *auth.Auth, model, clientToken, clientName string, counts *usage.Counts, start time.Time) {
	s.usage.Record(a.ID, a.Label, *counts)
	var costUSD float64
	var userID int64
	var multiplier float64
	if counts.Requests > 0 && clientToken != "" {
		official := s.pricing.Cost(auth.ProviderOpenAI, model, *counts)
		costUSD = official
		if info, ok := saasInfoFrom(c); ok && s.saas != nil {
			billed, err := s.saas.Charge(c.Request.Context(), info, auth.ProviderOpenAI, model, *counts, official)
			if err != nil {
				log.Warnf("saas: ws charge failed for token user=%d: %v", info.UserID, err)
			} else {
				costUSD = billed
				userID = info.UserID
				multiplier = s.saas.MultiplierFor(info, auth.ProviderOpenAI)
			}
		}
		s.usage.RecordClient(clientToken, clientName, *counts, costUSD)
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
		Status:      200,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      true,
		Path:        "/v1/responses",
	})
}

func closeCodexWS(conn *gorillaws.Conn, code int, reason string) {
	_ = conn.WriteControl(gorillaws.CloseMessage,
		gorillaws.FormatCloseMessage(code, reason),
		time.Now().Add(2*time.Second))
}

// codexWSUpstreamURL turns the configured ChatGPT backend base (https://...
// /backend-api) into the Codex responses WebSocket URL (wss://.../codex/responses).
func codexWSUpstreamURL(base string) string {
	u := strings.TrimRight(base, "/") + "/codex/responses"
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	default:
		return u
	}
}

// codexWSExtractModel best-effort reads the model from the first client frame,
// checking the top level and a nested "response" envelope.
func codexWSExtractModel(frame []byte) string {
	var probe struct {
		Model    string `json:"model"`
		Response struct {
			Model string `json:"model"`
		} `json:"response"`
	}
	if json.Unmarshal(frame, &probe) != nil {
		return ""
	}
	if probe.Model != "" {
		return probe.Model
	}
	return probe.Response.Model
}

// codexResponseID extracts response.id from a Codex backend event payload.
func codexResponseID(payload []byte) string {
	var ev struct {
		Response struct {
			ID string `json:"id"`
		} `json:"response"`
	}
	if json.Unmarshal(payload, &ev) != nil {
		return ""
	}
	return ev.Response.ID
}
