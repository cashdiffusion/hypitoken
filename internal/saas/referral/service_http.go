package referral

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// UserRoutes mounts the authenticated /referral/* routes (the invite + gift
// page). g is the RequireUser group.
func (s *Service) UserRoutes(g *gin.RouterGroup) {
	r := g.Group("/referral")
	r.GET("/me", s.handleMe)
	r.GET("/campaign", s.handleCampaign)
	r.GET("/cards", s.handleListCards)
	r.POST("/cards", s.handleMintCard)
	r.PATCH("/cards/:id", s.handleUpdateCard)
	r.POST("/cards/:id/impression", s.handleImpression)
	r.POST("/gifts", s.handleSendGift)
	r.GET("/gifts", s.handleListSentGifts)
	r.GET("/gifts/received", s.handleListReceivedGifts)
	r.POST("/gifts/claim", s.handleClaimGift)
}

// resolvedCopy applies the campaign's A/B split for a user (variant B for odd
// user ids when a B variant is configured), returning the chosen copy + label.
func (c *Campaign) resolvedCopy(userID int64) (headline, subcopy, variant string) {
	headline, subcopy, variant = c.Headline, c.Subcopy, "A"
	if c.VariantB == "" || userID%2 == 0 {
		return
	}
	var b struct {
		Headline string `json:"headline"`
		Subcopy  string `json:"subcopy"`
	}
	if err := json.Unmarshal([]byte(c.VariantB), &b); err != nil {
		return
	}
	variant = "B"
	if b.Headline != "" {
		headline = b.Headline
	}
	if b.Subcopy != "" {
		subcopy = b.Subcopy
	}
	return
}

func (s *Service) campaignPayload(c *gin.Context, userID int64) gin.H {
	camp, err := s.ActiveCampaign(c.Request.Context())
	if err != nil {
		return gin.H{}
	}
	headline, subcopy, variant := camp.resolvedCopy(userID)
	tiers, _ := s.ListTiers(c.Request.Context(), camp.ID)
	if tiers == nil {
		tiers = []*Tier{}
	}
	return gin.H{
		"id":                camp.ID,
		"slug":              camp.Slug,
		"name":              camp.Name,
		"kind":              camp.Kind,
		"invitee_bonus_usd": camp.InviteeBonusUSD,
		"inviter_bonus_usd": camp.InviterBonusUSD,
		"gift_expiry_days":  camp.GiftExpiryDays,
		"max_gift_usd":      camp.MaxGiftUSD,
		"headline":          headline,
		"subcopy":           subcopy,
		"variant":           variant,
		"tiers":             tiers,
	}
}

func (s *Service) handleMe(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	stats, err := s.PersonalStats(c.Request.Context(), u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	card, err := s.PrimaryCard(c.Request.Context(), u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"stats":       stats,
		"card":        card,
		"invite_url":  s.InviteURL(card.Code),
		"invite_code": card.Code,
		"campaign":    s.campaignPayload(c, u.ID),
	})
}

func (s *Service) handleCampaign(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	c.JSON(http.StatusOK, s.campaignPayload(c, u.ID))
}

func (s *Service) handleListCards(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	cards, err := s.ListCards(c.Request.Context(), u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cards == nil {
		cards = []*Card{}
	}
	out := make([]gin.H, 0, len(cards))
	for _, card := range cards {
		out = append(out, gin.H{"card": card, "invite_url": s.InviteURL(card.Code)})
	}
	c.JSON(http.StatusOK, gin.H{"cards": out})
}

type cardReq struct {
	CardStyle string `json:"card_style"`
	CardTone  string `json:"card_tone"`
	Tagline   string `json:"tagline"`
	Message   string `json:"message"`
}

func (s *Service) handleMintCard(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	var req cardReq
	_ = c.ShouldBindJSON(&req)
	card, err := s.MintCard(c.Request.Context(), u.ID, req.CardStyle, req.CardTone, req.Tagline, req.Message)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"card": card, "invite_url": s.InviteURL(card.Code)})
}

func (s *Service) handleUpdateCard(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var req cardReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	card, err := s.UpdateCard(c.Request.Context(), u.ID, id, req.CardStyle, req.CardTone, req.Tagline, req.Message)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "card not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"card": card, "invite_url": s.InviteURL(card.Code)})
}

func (s *Service) handleImpression(c *gin.Context) {
	// Best-effort share-impression beacon. Accepts the card id; resolves to the
	// owner's code so it can't be used to inflate someone else's counter.
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	card, err := s.PrimaryCard(c.Request.Context(), u.ID)
	if err == nil {
		s.TouchImpression(c.Request.Context(), card.Code)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type sendGiftReq struct {
	RecipientEmail string  `json:"recipient_email"`
	AmountUSD      float64 `json:"amount_usd"`
	Message        string  `json:"message"`
	CardStyle      string  `json:"card_style"`
	CardTone       string  `json:"card_tone"`
}

func (s *Service) handleSendGift(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	var req sendGiftReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	gift, err := s.SendGift(c.Request.Context(), u.ID, u.Email, req.RecipientEmail, req.AmountUSD, req.Message, req.CardStyle, req.CardTone)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidEmail), errors.Is(err, ErrSelfGift), errors.Is(err, ErrAmountTooSmall), errors.Is(err, ErrAmountTooLarge):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, db.ErrInsufficientBalance):
			c.JSON(http.StatusPaymentRequired, gin.H{"error": "insufficient balance"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	bal, _ := s.DB.GetBalance(c.Request.Context(), u.ID)
	c.JSON(http.StatusOK, gin.H{"gift": giftView(gift), "balance_usd": bal})
}

func (s *Service) handleListSentGifts(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	gifts, total, err := s.DB.ListGiftsBySender(c.Request.Context(), u.ID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"gifts": giftViews(gifts), "total": total, "limit": limit, "offset": offset})
}

func (s *Service) handleListReceivedGifts(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	gifts, total, err := s.DB.ListGiftsForRecipientEmail(c.Request.Context(), u.Email, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"gifts": giftViews(gifts), "total": total, "limit": limit, "offset": offset})
}

type claimGiftReq struct {
	Code string `json:"code"`
}

func (s *Service) handleClaimGift(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	// Rate-limit redeem attempts per (user, IP) so a redeem code can't be
	// brute-forced even though it's an 80-bit bearer secret.
	if s.claimRL != nil {
		if ok, retry := s.claimRL.allow(fmt.Sprintf("%d|%s", u.ID, c.ClientIP())); !ok {
			c.Header("Retry-After", strconv.Itoa(retry))
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many redeem attempts", "retry_after": retry})
			return
		}
	}
	var req claimGiftReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	gift, bal, err := s.ClaimGiftByCode(c.Request.Context(), req.Code, u.ID)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "兑换码无效"})
		case errors.Is(err, db.ErrGiftNotClaimable):
			c.JSON(http.StatusConflict, gin.H{"error": "该礼品卡已被领取或已过期"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"gift": giftView(gift), "balance_usd": bal})
}

// giftView renders a gift for the API, exposing the display-formatted redeem
// code rather than the raw stored form.
func giftView(g *db.GiftCard) gin.H {
	if g == nil {
		return gin.H{}
	}
	v := gin.H{
		"id":              g.ID,
		"code":            formatRedeem(g.Code),
		"recipient_email": g.RecipientEmail,
		"amount_usd":      g.AmountUSD,
		"message":         g.Message,
		"card_style":      g.CardStyle,
		"card_tone":       g.CardTone,
		"status":          g.Status,
		"created_at":      g.CreatedAt.Unix(),
	}
	if !g.ExpiresAt.IsZero() {
		v["expires_at"] = g.ExpiresAt.Unix()
	}
	if !g.ClaimedAt.IsZero() {
		v["claimed_at"] = g.ClaimedAt.Unix()
	}
	return v
}

func giftViews(gs []*db.GiftCard) []gin.H {
	out := make([]gin.H, 0, len(gs))
	for _, g := range gs {
		out = append(out, giftView(g))
	}
	return out
}
