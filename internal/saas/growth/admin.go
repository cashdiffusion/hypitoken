package growth

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// AdminRoutes mounts the operator CRUD + analytics endpoints under the given
// group (typically /api/v2/admin, already gated by RequireAdmin). All paths are
// namespaced under /growth so the module stays self-contained and easy to spot.
func (s *Service) AdminRoutes(g *gin.RouterGroup) {
	gr := g.Group("/growth")
	gr.GET("/channels", s.adminListChannels)
	gr.POST("/channels", s.adminCreateChannel)
	gr.PATCH("/channels/:id", s.adminUpdateChannel)
	gr.DELETE("/channels/:id", s.adminDeleteChannel)
	gr.GET("/analytics", s.adminAnalytics)
}

func (s *Service) adminListChannels(c *gin.Context) {
	chs, err := s.ListChannels(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if chs == nil {
		chs = []*Channel{}
	}
	c.JSON(http.StatusOK, gin.H{"channels": chs})
}

// channelReq is the JSON body for create/update. Slug is required on create and
// ignored on update (immutable). Enabled defaults to true on create when the
// field is omitted.
type channelReq struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	BonusUSD    float64 `json:"bonus_usd"`
	Enabled     *bool   `json:"enabled"`
}

func (r channelReq) enabledOrDefault() bool {
	if r.Enabled == nil {
		return true
	}
	return *r.Enabled
}

func (s *Service) adminCreateChannel(c *gin.Context) {
	var req channelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	slug := NormalizeSlug(req.Slug)
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid slug — use lowercase letters, digits, '-' or '_' (max 31 chars)"})
		return
	}
	if req.BonusUSD < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bonus_usd must be >= 0"})
		return
	}
	ch, err := s.CreateChannel(c.Request.Context(), ChannelParams{
		Slug:        slug,
		Name:        req.Name,
		Description: req.Description,
		BonusUSD:    req.BonusUSD,
		Enabled:     req.enabledOrDefault(),
	})
	if err != nil {
		// Most likely a duplicate slug (UNIQUE constraint).
		c.JSON(http.StatusConflict, gin.H{"error": "could not create channel (slug already exists?): " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, ch)
}

func (s *Service) adminUpdateChannel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var req channelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if req.BonusUSD < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bonus_usd must be >= 0"})
		return
	}
	ch, err := s.UpdateChannel(c.Request.Context(), id, ChannelParams{
		Name:        req.Name,
		Description: req.Description,
		BonusUSD:    req.BonusUSD,
		Enabled:     req.enabledOrDefault(),
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "channel not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, ch)
}

func (s *Service) adminDeleteChannel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	if err := s.DeleteChannel(c.Request.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "channel not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// adminAnalytics returns the full attribution rollup the admin tab renders:
// headline totals, per-channel stats (visits/signups/conversion/dwell/ROI), and
// a visits-vs-signups timeseries.
func (s *Service) adminAnalytics(c *gin.Context) {
	ctx := c.Request.Context()
	days, _ := strconv.Atoi(c.DefaultQuery("days", "14"))
	if days <= 0 || days > 90 {
		days = 14
	}
	totals, err := s.Totals(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	stats, err := s.Stats(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if stats == nil {
		stats = []*ChannelStats{}
	}
	series, err := s.Timeseries(ctx, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"totals":   totals,
		"channels": stats,
		"daily":    series,
	})
}
