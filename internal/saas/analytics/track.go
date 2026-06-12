package analytics

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// PublicRoutes mounts the unauthenticated tracking endpoints under the given
// group (typically /api/v2). They are intentionally cheap and fire-and-forget:
// the browser beacons here on landing, on every SPA navigation, on key CTA
// clicks, and on page hide — so they must never block or error in a way the
// visitor notices. Sits alongside growth's /track/{visit,ping}; the distinct
// /track/{event,dwell} paths don't collide.
func (s *Service) PublicRoutes(g *gin.RouterGroup) {
	t := g.Group("/track")
	t.POST("/event", s.trackEvent)
	t.POST("/dwell", s.trackDwell)
}

type eventReq struct {
	Sid      string `json:"sid"`      // session id (sessionStorage, one per tab session)
	Vid      string `json:"vid"`      // anonymous visitor id (localStorage)
	Kind     string `json:"kind"`     // "pageview" | "action"
	Name     string `json:"name"`     // pageview: page label; action: CTA id (start/login/register/…)
	Path     string `json:"path"`     // landing path — only consumed when the session is first created
	Referrer string `json:"referrer"` // document.referrer — only consumed on session creation
}

// trackEvent records a pageview or action. The first event of a session creates
// the session row (capturing landing page + acquisition source); every event
// appends to the flow log and advances the session counters. Always returns 200
// with {ok} — the client doesn't care and we don't want to leak anything.
func (s *Service) trackEvent(c *gin.Context) {
	var req eventReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	sid := sanitizeID(req.Sid)
	vid := sanitizeID(req.Vid)
	if req.Kind != "pageview" && req.Kind != "action" {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	name := sanitizeName(req.Name)
	if sid == "" || vid == "" || name == "" {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	source, domain := classifySource(req.Referrer, c.Request.Host)
	if err := s.RecordEvent(c.Request.Context(), sid, vid, req.Kind, name, sanitizeName(req.Path), source, domain); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type dwellReq struct {
	Sid string `json:"sid"`
	Vid string `json:"vid"`
	MS  int64  `json:"ms"` // total elapsed dwell time this session
}

// trackDwell accumulates dwell time for an existing session. Sent periodically
// as a heartbeat and once more via navigator.sendBeacon on page hide. Clamped
// and best-effort; never creates a session (RecordEvent owns that), so a stray
// ping for an unknown session is a harmless no-op UPDATE.
func (s *Service) trackDwell(c *gin.Context) {
	var req dwellReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	sid := sanitizeID(req.Sid)
	if sid == "" {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	ms := req.MS
	if ms < 0 {
		ms = 0
	}
	if ms > maxDwellMS {
		ms = maxDwellMS
	}
	if err := s.AccumulateDwell(c.Request.Context(), sid, ms); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
