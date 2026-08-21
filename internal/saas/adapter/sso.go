// Cross-origin single-sign-on handoff — the minting half.
//
// The sibling HypiHub gateway runs on a different origin (hub.novadiffusion.com
// vs api.novadiffusion.com), so it cannot see the JWT this site holds in
// localStorage and there is no cookie spanning both. POST /api/v2/auth/sso/code
// closes that gap: an already-authenticated user asks for a 120-second one-time
// code, the browser carries it across in one redirect, and HypiHub redeems it
// server-to-server at /api/v2/svc/sso/redeem (service.go) for a normal JWT.
//
// Additive: the route exists only while saas.sso_return_origins is non-empty,
// and it changes nothing about how any existing route authenticates.
package adapter

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/saas"
	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// ssoCodeRPM caps how many handoff codes one user may mint per minute.
//
// The legitimate shape of this call is one code per login-and-bounce, so 20/min
// is already generous. The cap is there because each call is a row insert
// driven by an authenticated but otherwise unprivileged client, and because a
// client stuck in a redirect loop (HypiHub failing to redeem, SPA retrying)
// would otherwise mint codes as fast as it can round-trip.
const ssoCodeRPM = 20

// maxReturnURLLen bounds what may land in sso_codes.return_url.
//
// The origin is pinned by the allowlist; the PATH and QUERY are not, and they
// are written verbatim into saas.db by an authenticated but unprivileged
// caller. Without a bound, 20 calls a minute (the limiter below) can push
// arbitrary megabytes into a file this deployment has already lost once to
// corruption, and each row survives its 120-second life plus the pruner's
// one-hour grace. 2048 is past anything a real redirect target needs.
const maxReturnURLLen = 2048

// checkReturnURLShape is the second half of the return_url gate, and it runs
// BEFORE the origin check so its verdict is the same for every origin.
//
// The allowlist constrains the ORIGIN. It says nothing about the query string,
// and the browser is about to append `code=<the session>` to whatever we echo
// back — so two shapes have to be refused here:
//
//   - a URL that already carries `code=`. The client sets the parameter through
//     the URL API now, which replaces rather than appends, but if that ever
//     regresses to concatenation the far side reads `?code=attacker&code=victim`
//     and returns the FIRST — signing the victim's browser into the sibling
//     product as the attacker. One line here means the exploit needs two
//     independent mistakes, not one.
//   - a fragment. `#` cannot carry the code to a server and its only effect is
//     to move the parameter somewhere the destination will not read, which is a
//     handoff that silently fails rather than one that visibly refuses.
func checkReturnURLShape(raw string) bool {
	if len(raw) > maxReturnURLLen {
		return false
	}
	if strings.Contains(raw, "#") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return !u.Query().Has("code")
}

// ssoCodeTTLSeconds is what the response advertises. Kept in lockstep with
// db.SSOCodeTTL — the browser uses it to decide whether a stashed code is
// still worth spending.
var ssoCodeTTLSeconds = int(db.SSOCodeTTL / time.Second)

// SSOHandler mints handoff codes. Holds the parsed allowlist (built once at
// startup) and a per-user rate limiter; one instance is mounted on every gin
// engine, so the limiter is shared across them by construction.
type SSOHandler struct {
	DB      *db.DB
	Origins *saas.OriginAllowlist

	limiter *userRateLimiter
}

// NewSSOHandler returns nil when there is no store or no configured origin.
//
// A nil handler mounts nothing. That is deliberately how "no sibling site" is
// expressed: an unconfigured deployment should not have an SSO endpoint at all,
// rather than one that exists and rejects everything — a route that 400s is a
// route an attacker knows to keep watching.
func NewSSOHandler(store *db.DB, origins *saas.OriginAllowlist) *SSOHandler {
	if store == nil || origins.Len() == 0 {
		return nil
	}
	log.Infof("saas: cross-site SSO handoff enabled for %v", origins.Origins())
	return &SSOHandler{DB: store, Origins: origins, limiter: newUserRateLimiter()}
}

// AuthedRoutes registers the minting endpoint on a group that has ALREADY been
// through RequireUser. Nil-safe.
//
// It must stay on the authed group: the code it hands out is a session, so the
// only person who may ask for one is the person already holding that session.
func (h *SSOHandler) AuthedRoutes(g *gin.RouterGroup) {
	if h == nil {
		return
	}
	g.POST("/auth/sso/code", h.issueCode)
}

type ssoCodeReq struct {
	ReturnURL string `json:"return_url"`
}

func (h *SSOHandler) issueCode(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		// RequireUser already ran, so this is unreachable in practice; refuse
		// rather than mint a code for nobody.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	if !h.limiter.allow(u.ID, ssoCodeRPM, time.Minute) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many sso code requests"})
		return
	}

	var req ssoCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "malformed JSON body"})
		return
	}

	// Shape first, so the answer does not depend on the origin. See
	// checkReturnURLShape.
	if !checkReturnURLShape(strings.TrimSpace(req.ReturnURL)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "return_url must be a bare destination URL " +
			"with no fragment and no code parameter"})
		return
	}

	// The whole security boundary. Exact-origin match; see
	// saas.OriginAllowlist.Allows.
	returnURL, ok := h.Origins.Allows(req.ReturnURL)
	if !ok {
		// The rejected URL is NOT echoed. It is attacker-chosen text, and an
		// error body that reflects it is a small XSS/phishing surface for
		// anything that renders the message — and a confirmation oracle for
		// probing the allowlist. The operator can read the configured origins
		// from the startup log; the client does not need to be told what it
		// sent.
		c.JSON(http.StatusBadRequest, gin.H{"error": "return_url is not an allowed origin"})
		return
	}

	code, err := h.DB.CreateSSOCode(c.Request.Context(), u.ID, returnURL, db.SSOCodeTTL)
	if err != nil {
		log.Errorf("saas sso: minting handoff code for user %d failed: %v", u.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not issue sso code"})
		return
	}
	// The raw code appears here and nowhere else — not in the request log, not
	// in any log line above.
	c.JSON(http.StatusOK, gin.H{
		"code":       code,
		"expires_in": ssoCodeTTLSeconds,
		"return_url": returnURL,
	})
}

// userRateLimiter is a fixed-window per-user counter.
//
// In-memory, like the auth package's codeRateLimiter, and for the same reason:
// this is a single-process deployment. It bounds abuse of an authenticated
// endpoint, so it does not need to be exact — a window boundary letting through
// 2x the nominal rate is irrelevant against a limit whose honest usage is 1.
type userRateLimiter struct {
	mu      sync.Mutex
	windows map[int64]*rateWindow
}

type rateWindow struct {
	start time.Time
	count int
}

func newUserRateLimiter() *userRateLimiter {
	return &userRateLimiter{windows: make(map[int64]*rateWindow)}
}

func (l *userRateLimiter) allow(userID int64, limit int, window time.Duration) bool {
	if l == nil || limit <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// Evict stale windows opportunistically. The map is keyed by user id, so
	// it is bounded by the number of users who used SSO in the last minute —
	// but an unbounded map fed by request traffic is worth never having.
	if len(l.windows) > 4096 {
		for k, w := range l.windows {
			if now.Sub(w.start) >= window {
				delete(l.windows, k)
			}
		}
	}

	w, ok := l.windows[userID]
	if !ok || now.Sub(w.start) >= window {
		l.windows[userID] = &rateWindow{start: now, count: 1}
		return true
	}
	if w.count >= limit {
		return false
	}
	w.count++
	return true
}
