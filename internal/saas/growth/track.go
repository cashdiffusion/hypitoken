package growth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// maxDwellMS caps a single reported dwell time at 6 hours. Beyond that the
// figure is almost certainly a backgrounded tab or a tampered client; clamping
// keeps one bad sample from skewing the average.
const maxDwellMS = int64(6 * 60 * 60 * 1000)

// PublicRoutes mounts the unauthenticated tracking endpoints under the given
// group (typically /api/v2). They are intentionally cheap and fire-and-forget:
// the browser beacons here on landing and on unload, so they must never block
// or error in a way the user notices. Unknown / disabled channels are silently
// ignored — no row is created for an arbitrary ?ref= value, which keeps the
// public surface from being used to spam the table.
func (s *Service) PublicRoutes(g *gin.RouterGroup) {
	t := g.Group("/track")
	t.POST("/visit", s.trackVisit)
	t.POST("/ping", s.trackPing)
}

type visitReq struct {
	Ref string `json:"ref"`
	Vid string `json:"vid"`
}

// trackVisit records a first-touch visit for (channel, visitor). Always returns
// 200 with {ok:true} even when the ref is unknown — the client doesn't care and
// we don't want to leak which slugs exist.
func (s *Service) trackVisit(c *gin.Context) {
	var req visitReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	slug := NormalizeSlug(req.Ref)
	vid := sanitizeVisitorID(req.Vid)
	if slug == "" || vid == "" {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	// Only track real, enabled channels so the table can't be stuffed with
	// junk slugs.
	if ch, err := s.GetChannelBySlug(c.Request.Context(), slug); err != nil || !ch.Enabled {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	if err := s.RecordVisit(c.Request.Context(), slug, vid); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type pingReq struct {
	Ref string `json:"ref"`
	Vid string `json:"vid"`
	MS  int64  `json:"ms"` // total elapsed dwell time this session
}

// trackPing accumulates dwell time for an existing visit. Sent periodically as
// a heartbeat and once more via navigator.sendBeacon on page hide. Clamped and
// best-effort; never creates a visit (RecordVisit owns that) so a stray ping
// for an unknown visitor is a harmless no-op UPDATE.
func (s *Service) trackPing(c *gin.Context) {
	var req pingReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	slug := NormalizeSlug(req.Ref)
	vid := sanitizeVisitorID(req.Vid)
	if slug == "" || vid == "" {
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
	if err := s.AccumulateDwell(c.Request.Context(), slug, vid, ms); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
