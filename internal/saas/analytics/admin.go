package analytics

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// AdminRoutes mounts the operator analytics endpoint under the given group
// (typically /api/v2/admin, already gated by RequireAdmin). Namespaced under
// /analytics so the module stays self-contained and distinct from growth's
// /growth group.
func (s *Service) AdminRoutes(g *gin.RouterGroup) {
	gr := g.Group("/analytics")
	gr.GET("/overview", s.adminOverview)
}

// adminOverview returns the visitor-behaviour rollup the admin "Growth" tab
// renders below the channel stats.
func (s *Service) adminOverview(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "14"))
	if days <= 0 || days > 90 {
		days = 14
	}
	ov, err := s.Overview(c.Request.Context(), days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Normalize nil slices to empty so the JSON is always arrays, not null.
	if ov.FirstActions == nil {
		ov.FirstActions = []*Bucket{}
	}
	if ov.Sources == nil {
		ov.Sources = []*Bucket{}
	}
	if ov.Referrers == nil {
		ov.Referrers = []*Bucket{}
	}
	if ov.Paths == nil {
		ov.Paths = []*PathCount{}
	}
	c.JSON(http.StatusOK, ov)
}
