package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/wjsoj/cc-core/auth"
)

// What a ChatGPT-backend 401 means, and why the Codex path used to loop on it.
//
// On 2026-09-03 one Pro credential started answering every request with
// 401 {"code":"token_expired"} while its local JWT still had six days to live.
// The backend had invalidated the access token server-side; nothing about the
// file said so. EnsureFresh only looks at the local expiry, so no proactive
// refresh ever fired, and ReportUpstreamError's 401 rule is a one-minute
// cooldown with no memory — so the credential came back every minute, ate one
// upstream round-trip (a socks dial plus the request) from whichever customer
// drew it, 401'd again, and went back to sleep. 23 times in the first twelve
// minutes, for as long as anyone cared to wait. The Anthropic path had grown
// the right shape long ago (proxy.go, the 401 case): count the strike on
// Consecutive401s so a sustained run promotes to a sticky hard-failure, and
// treat a single one as recoverable. Codex just never got it.
//
// The recovery here is what the vendor Codex CLI does on a 401: re-mint the
// access token from the refresh token and carry on. Two guards make that safe
// under load. The bearer this request sent is compared against the credential's
// current one, so once one goroutine has rotated the token the others see a
// different bearer and skip; and the comparison happens under a per-credential
// mutex, so two goroutines that both saw the stale bearer cannot both refresh.
// The second guard matters: Codex refresh tokens rotate on use, and cc-core
// hard-fails a credential whose refresh token is presented twice.
//
// If the refresh itself is rejected, cc-core has already marked the credential
// hard-failed (codex_refresh.go) and there is nothing more to do here. If it
// succeeds but the new token is rejected too, Consecutive401s keeps climbing
// and auth401HardFailureThreshold retires the account — a run of rejections
// that survived a refresh is a revoked account, not a rotation race.

// forceRefreshLeeway is handed to EnsureFresh so needsRefresh is true whatever
// the local expiry says: the backend has told us the token is dead, and its
// opinion outranks the JWT's exp claim.
const forceRefreshLeeway = time.Duration(1<<63 - 1)

// codexRefreshTimeout bounds the forced exchange. It runs detached from the
// customer's request context (the customer is being failed over to another
// credential and may well hang up) but must not outlive a stuck upstream.
const codexRefreshTimeout = 20 * time.Second

// refreshGate serialises forced refreshes per credential id.
type refreshGate struct {
	locks sync.Map // auth id → *sync.Mutex
}

func (g *refreshGate) lock(id string) *sync.Mutex {
	v, _ := g.locks.LoadOrStore(id, &sync.Mutex{})
	mu, _ := v.(*sync.Mutex)
	mu.Lock()
	return mu
}

// recoverRejectedToken re-mints a credential's access token after the upstream
// rejected `seen`, unless someone already has. current reports the token the
// credential holds right now; refresh performs the exchange. It returns
// rotated=true when the credential now carries a token other than `seen`,
// whether this call minted it or another one did.
func recoverRejectedToken(gate *refreshGate, id, seen string, current func() string, refresh func() error) (rotated bool, err error) {
	mu := gate.lock(id)
	defer mu.Unlock()
	if current() != seen {
		return true, nil
	}
	if err := refresh(); err != nil {
		return false, err
	}
	return current() != seen, nil
}

// codexForceRefresh is the production exchange. Tests substitute Server.codexRefresh.
func (s *Server) codexForceRefresh(ctx context.Context, a *auth.Auth) error {
	if s.codexRefresh != nil {
		return s.codexRefresh(ctx, a)
	}
	return a.EnsureFresh(ctx, forceRefreshLeeway, s.cfg.UseUTLS)
}

// rejectCodexBearer is the Codex-side reaction to an upstream 401: count the
// strike, try to re-mint the token, and decide whether the credential needs a
// cooldown. `seen` is the bearer the rejected request carried. A credential
// whose token was re-minted gets no cooldown and is routable again at once.
func (s *Server) rejectCodexBearer(a *auth.Auth, seen string, errBody []byte) {
	n := a.MarkAuthRejection(fmt.Sprintf("upstream 401 authentication rejected: %s", truncate(errBody, 200)))
	if a.IsHardFailed() {
		log.Warnf("auth: %s hard-disabled — %d consecutive 401s with no success (presumed revoked)", a.ID, n)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexRefreshTimeout)
	defer cancel()
	rotated, err := recoverRejectedToken(&s.codexRefreshGate, a.ID, seen,
		func() string { tok, _ := a.Credentials(); return tok },
		func() error { return s.codexForceRefresh(ctx, a) })
	switch {
	case err != nil:
		// cc-core has already hard-failed the credential if the refresh token
		// was rejected; anything else is transient and gets the plain cooldown.
		log.Warnf("codex oauth: %s rejected the bearer (strike %d) and the forced refresh failed: %v", a.ID, n, err)
		s.pool.ReportUpstreamError(a, 401, time.Time{})
	case rotated:
		log.Warnf("codex oauth: %s rejected the bearer (strike %d); access token re-minted, back in rotation", a.ID, n)
	default:
		// No refresh token to work with. Same one-minute cooldown as before,
		// but now the strike counter is running, so it ends.
		log.Warnf("codex oauth: %s rejected the bearer (strike %d) and cannot refresh — cooling down", a.ID, n)
		s.pool.ReportUpstreamError(a, 401, time.Time{})
	}
}
