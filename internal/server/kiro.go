package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/kiroapi"
	"github.com/wjsoj/cc-core/kirobridge"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/usage"

	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/kirocreds"
	"github.com/wjsoj/CPA-Claude/internal/kiroupstream"
)

// KiroState bundles the Kiro side state (credentials + dispatcher + PKCE
// sessions). Stored on Server.kiro. Initialized lazily; nil when no Kiro
// configuration is present.
//
// Field names use the unexported prefix `kc/pkc/disp` so the admin.KiroAccess
// interface methods (Store(), PKCE()) don't collide.
type KiroState struct {
	store *kirocreds.Store
	pkce  *kirocreds.PKCESessions
	disp  *kiroupstream.Dispatcher
}

// InitKiro spins up the Kiro side. Errors here are fatal at startup — a
// misconfigured Kiro store would silently 503 every kiro-routed request,
// which is worse than refusing to start.
//
// Returns nil err when Kiro is intentionally disabled (no token_groups
// declare upstream=kiro).
func (s *Server) InitKiro() error {
	useKiro := false
	for _, g := range s.cfg.TokenGroups {
		if g.Upstream == config.UpstreamKiro {
			useKiro = true
			break
		}
	}
	if !useKiro {
		return nil
	}
	store, err := kirocreds.Open(s.cfg.KiroAuthDir)
	if err != nil {
		return fmt.Errorf("kiro: open store: %w", err)
	}
	s.kiro = &KiroState{
		store: store,
		pkce:  kirocreds.NewPKCESessions(),
		disp:  &kiroupstream.Dispatcher{Store: store},
	}
	log.Infof("kiro: enabled with %d stored credentials at %s", len(s.kiro.store.List()), s.cfg.KiroAuthDir)
	return nil
}

// KiroEnabled reports whether the Kiro side has been initialized.
func (s *Server) KiroEnabled() bool { return s.kiro != nil }

// KiroAccess exposes the Kiro state for the admin handler. nil when disabled.
func (s *Server) KiroAccess() *KiroState { return s.kiro }

// Store + PKCE satisfy the admin.KiroAccess interface so the legacy admin
// handler can mount its /api/kiro/* routes without an import cycle.
func (k *KiroState) Store() *kirocreds.Store       { return k.store }
func (k *KiroState) PKCE() *kirocreds.PKCESessions { return k.pkce }

// chooseKiroGroupFor returns the first token-group from groups whose
// upstream is "kiro" AND whose Models whitelist allows model. Returns nil
// when no such group exists.
//
// Walks groups in declared priority order — that's the user-defined
// "fallthrough priority".
func (s *Server) chooseKiroGroupFor(groups []string, model string) *config.TokenGroup {
	for _, name := range groups {
		g := s.cfg.FindTokenGroup(name)
		if g == nil || g.Upstream != config.UpstreamKiro {
			continue
		}
		if !g.AcceptsModel(model) {
			continue
		}
		return g
	}
	return nil
}

// tryKiro is invoked from forward() right after token resolution. If the
// resolved token has a Kiro-routed group that's healthy AND accepts the
// model, this dispatches the request through kiroupstream and returns
// handled=true. Otherwise returns handled=false so the caller falls through
// to the existing Anthropic path.
//
// Side effects: writes the response body to c, records usage + emits the
// request log with multiplier=group.Discount.
func (s *Server) tryKiro(
	c *gin.Context,
	body []byte,
	model string,
	stream bool,
	clientToken, clientName, path string,
	clientGroups []string,
	start time.Time,
) (handled bool) {
	if s.kiro == nil || len(clientGroups) == 0 {
		return false
	}
	group := s.chooseKiroGroupFor(clientGroups, model)
	if group == nil {
		return false
	}
	chosen, err := s.kiro.disp.PickCredential(c.Request.Context(), nil)
	if err != nil {
		// No healthy Kiro creds → caller falls through to next group / anthropic.
		log.Warnf("kiro: pick credential failed (will fall through): %v", err)
		return false
	}
	// PickCredential acquired a per-credential concurrency slot — release it
	// when the request finishes (success OR mid-stream failure).
	defer s.kiro.store.Release(chosen.Entry.ID)

	// Parse the Anthropic-format body into the typed struct kirobridge expects.
	var areq kirobridge.AnthropicRequest
	if err := json.Unmarshal(body, &areq); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "kiro: parse request: " + err.Error()})
		return true
	}
	areq.Stream = stream
	if areq.Model == "" {
		areq.Model = model
	}

	counts, _, derr := s.kiro.disp.Forward(c, c.Request.Context(), &areq, chosen.Entry, chosen.Entry.Cred.ProfileARN)
	if derr != nil {
		// Decide a client-visible status. If kiroapi returned an HTTPError we
		// surface its upstream status so the client sees 429 as 429 (not 500
		// + empty body, which made Claude Code blow up parsing usage tokens).
		status := 502
		if errors.Is(derr, context.Canceled) {
			status = 499
		}
		var herr *kiroapi.HTTPError
		if errors.As(derr, &herr) {
			status = herr.StatusCode
		}
		s.emitLog(requestlog.Record{
			Client: clientName, ClientToken: maskClientToken(clientToken),
			Provider:  auth.ProviderAnthropic,
			AuthID:    "kiro:" + chosen.Entry.ID + "(" + group.Name + ")",
			AuthLabel: chosen.Entry.Label,
			AuthKind:  "kiro",
			Model:     model, Stream: stream, Path: path,
			Status: status, DurationMs: time.Since(start).Milliseconds(),
			Error:  derr.Error(),
		})
		// Only write a response when nothing has been sent yet — Forward
		// returns errors BEFORE writeSSE/writeOneShot fires, so this is the
		// common case. Skip when bytes are already on the wire (mid-stream).
		if !c.Writer.Written() {
			c.AbortWithStatusJSON(status, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "upstream_error",
					"message": derr.Error(),
				},
			})
		}
		return true
	}

	// Billing: official Anthropic price × group.Discount.
	counts.Requests = 1
	officialCost := s.pricing.Cost(auth.ProviderAnthropic, model, counts)
	billedCost := officialCost * group.Discount

	// Persist usage on the credential-level and client-level ledgers. The
	// credential ledger uses a synthetic ID so admin status pages can
	// segregate kiro vs anthropic upstream consumption.
	authID := "kiro:" + chosen.Entry.ID
	authLabel := chosen.Entry.Label
	if authLabel == "" {
		authLabel = chosen.Entry.ID
	}
	s.usage.Record(authID, authLabel, counts)
	if clientToken != "" {
		s.usage.RecordClient(clientToken, clientName, counts, billedCost)
	}

	// Record the discounted figure as CostUSD (matches what was actually
	// debited from the wallet). BilledUSD = same as CostUSD here (we don't
	// double-discount via SaaS for the Kiro path). Multiplier records the
	// discount factor so audits can recompute the official price.
	// Encode the group name into AuthID so admin filters can split by group.
	s.emitLog(requestlog.Record{
		Client:      clientName,
		ClientToken: maskClientToken(clientToken),
		Provider:    auth.ProviderAnthropic,
		AuthID:      authID + "(" + group.Name + ")",
		AuthLabel:   authLabel,
		AuthKind:    "kiro",
		Model:       model,
		Input:       counts.InputTokens,
		Output:      counts.OutputTokens,
		CacheRead:   counts.CacheReadTokens,
		CacheCreate: counts.CacheCreateTokens,
		CostUSD:     officialCost,
		BilledUSD:   billedCost,
		Multiplier:  group.Discount,
		Status:      200,
		DurationMs:  time.Since(start).Milliseconds(),
		Stream:      stream,
		Path:        path,
		Attempts:    1,
	})
	return true
}

// counts2json is a small helper for diagnostics.
func counts2json(c usage.Counts) string {
	b, _ := json.Marshal(c)
	return string(b)
}
