package referral

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/growth"
)

// AdminRoutes mounts the operator CRUD + analytics under the given group
// (typically /api/v2/admin, already gated by RequireAdmin). Namespaced under
// /referral so the module stays self-contained.
func (s *Service) AdminRoutes(g *gin.RouterGroup) {
	r := g.Group("/referral")
	r.GET("/campaigns", s.adminListCampaigns)
	r.POST("/campaigns", s.adminCreateCampaign)
	r.PATCH("/campaigns/:id", s.adminUpdateCampaign)
	r.GET("/campaigns/:id/tiers", s.adminListTiers)
	r.POST("/campaigns/:id/tiers", s.adminCreateTier)
	r.DELETE("/tiers/:id", s.adminDeleteTier)
	r.GET("/analytics", s.adminAnalytics)
}

func (s *Service) adminListCampaigns(c *gin.Context) {
	camps, err := s.ListCampaigns(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if camps == nil {
		camps = []*Campaign{}
	}
	c.JSON(http.StatusOK, gin.H{"campaigns": camps})
}

type campaignReq struct {
	Slug               string  `json:"slug"`
	Name               string  `json:"name"`
	Kind               string  `json:"kind"`
	Status             string  `json:"status"`
	InviteeBonusUSD    float64 `json:"invitee_bonus_usd"`
	InviterBonusUSD    float64 `json:"inviter_bonus_usd"`
	InviterRewardOn    string  `json:"inviter_reward_on"`
	GiftExpiryDays     int     `json:"gift_expiry_days"`
	MaxGiftUSD         float64 `json:"max_gift_usd"`
	MaxRewardedInvites int     `json:"max_rewarded_invites"`
	DailyBudgetUSD     float64 `json:"daily_budget_usd"`
	StartsAt           int64   `json:"starts_at"`
	EndsAt             int64   `json:"ends_at"`
	Headline           string  `json:"headline"`
	Subcopy            string  `json:"subcopy"`
	VariantB           string  `json:"variant_b"`
}

func (r campaignReq) params() CampaignParams {
	// campaignReq and CampaignParams share an identical field set (the request
	// type only adds JSON tags), so a direct conversion is exact.
	return CampaignParams(r)
}

func (s *Service) adminCreateCampaign(c *gin.Context) {
	var req campaignReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	slug := growth.NormalizeSlug(req.Slug)
	if slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid slug — use lowercase letters, digits, '-' or '_' (max 31 chars)"})
		return
	}
	if req.InviteeBonusUSD < 0 || req.InviterBonusUSD < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bonus must be >= 0"})
		return
	}
	p := req.params()
	p.Slug = slug
	camp, err := s.CreateCampaign(c.Request.Context(), p)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "could not create campaign (slug already exists?): " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, camp)
}

func (s *Service) adminUpdateCampaign(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var req campaignReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if req.InviteeBonusUSD < 0 || req.InviterBonusUSD < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bonus must be >= 0"})
		return
	}
	camp, err := s.UpdateCampaign(c.Request.Context(), id, req.params())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "campaign not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, camp)
}

func (s *Service) adminListTiers(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	tiers, err := s.ListTiers(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if tiers == nil {
		tiers = []*Tier{}
	}
	c.JSON(http.StatusOK, gin.H{"tiers": tiers})
}

type tierReq struct {
	Threshold       int     `json:"threshold"`
	TierName        string  `json:"tier_name"`
	CardStyleUnlock string  `json:"card_style_unlock"`
	BonusUSD        float64 `json:"bonus_usd"`
	Badge           string  `json:"badge"`
}

func (s *Service) adminCreateTier(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var req tierReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if req.Threshold <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "threshold must be > 0"})
		return
	}
	tier, err := s.CreateTier(c.Request.Context(), id, req.Threshold, req.TierName, req.CardStyleUnlock, req.BonusUSD, req.Badge)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, tier)
}

func (s *Service) adminDeleteTier(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	if err := s.DeleteTier(c.Request.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "tier not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (s *Service) adminAnalytics(c *gin.Context) {
	stats, err := s.OpsStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}
