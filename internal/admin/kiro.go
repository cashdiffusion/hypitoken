package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/cc-core/kiroapi"
	"github.com/wjsoj/cc-core/kirotransport"

	"github.com/wjsoj/CPA-Claude/internal/kirocreds"
)

// KiroAccess is the subset of *server.KiroState the admin Handler needs.
// Stored as an interface so admin/ doesn't import internal/server/ (cycle).
type KiroAccess interface {
	Store() *kirocreds.Store
	PKCE() *kirocreds.PKCESessions
}

// SetKiroAccess wires the Kiro state into the admin Handler AND registers
// the /api/kiro/* routes if Register has already run. Call from main.go
// after server.InitKiro returns; pass nil to disable.
func (h *Handler) SetKiroAccess(k KiroAccess) {
	h.kiro = k
	if k != nil && h.apiGroup != nil {
		h.RegisterKiro(h.apiGroup)
	}
}

// RegisterKiro adds the /api/kiro/* admin endpoints to api. Call this
// inside Register() (or right after) — gated behind the same adminAuth().
// No-op when SetKiroAccess wasn't called.
func (h *Handler) RegisterKiro(api *gin.RouterGroup) {
	if h.kiro == nil {
		return
	}
	g := api.Group("/kiro")
	g.GET("/credentials", h.handleKiroList)
	g.DELETE("/credentials/:id", h.handleKiroDelete)
	g.PATCH("/credentials/:id", h.handleKiroPatch)
	g.GET("/credentials/:id/credits", h.handleKiroCredits)
	g.POST("/login/start", h.handleKiroLoginStart)
	// PKCE callback. Form-style + JSON both accepted so callers can
	// either POST with browser-form-style application/x-www-form-urlencoded
	// or relay the captured query directly as JSON.
	g.POST("/login/finish", h.handleKiroLoginFinish)
}

// Public DTOs

type kiroCredView struct {
	ID         string    `json:"id"`
	Label      string    `json:"label,omitempty"`
	Group      string    `json:"group,omitempty"`
	Disabled   bool      `json:"disabled"`
	CreatedAt  time.Time `json:"created_at"`
	ProfileARN string    `json:"profile_arn,omitempty"`
	Email      string    `json:"email,omitempty"`
	Plan       string    `json:"plan,omitempty"`
	MaskedToken string   `json:"masked_token,omitempty"`
	ExpiresAt  string    `json:"expires_at,omitempty"`
}

func (h *Handler) handleKiroList(c *gin.Context) {
	out := make([]kiroCredView, 0)
	for _, e := range h.kiro.Store().List() {
		out = append(out, kiroCredView{
			ID:          e.ID,
			Label:       e.Label,
			Group:       e.Group,
			Disabled:    e.Disabled,
			CreatedAt:   e.CreatedAt,
			ProfileARN:  e.Cred.ProfileARN,
			Email:       e.Cred.Email,
			Plan:        e.Cred.SubscriptionTier,
			MaskedToken: e.MaskedToken(),
			ExpiresAt:   e.Cred.ExpiresAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"credentials": out})
}

type kiroPatchBody struct {
	Label    *string `json:"label"`
	Group    *string `json:"group"`
	Disabled *bool   `json:"disabled"`
}

func (h *Handler) handleKiroPatch(c *gin.Context) {
	id := c.Param("id")
	var body kiroPatchBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	e, err := h.kiro.Store().Update(id, body.Label, body.Group, body.Disabled)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": e.ID})
}

func (h *Handler) handleKiroDelete(c *gin.Context) {
	id := c.Param("id")
	if err := h.kiro.Store().Delete(id); err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleKiroCredits hits Kiro's per-credential getUsageLimits endpoint
// and surfaces plan + used + limit + remaining so the admin can show
// per-credential balance cards.
func (h *Handler) handleKiroCredits(c *gin.Context) {
	id := c.Param("id")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	entry, err := h.kiro.Store().EnsureFresh(ctx, id, 5*time.Minute)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	kc := &kiroapi.Client{
		Token:    entry.Cred.AccessToken,
		IsAPIKey: entry.Cred.IsAPIKey(),
		Flavor:   kirotransport.FlavorCLI,
	}
	r, err := kc.GetCredits(ctx, entry.Cred.ProfileARN)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"plan":      r.Plan(),
		"used":      r.UsageTotal(),
		"limit":     r.LimitTotal(),
		"remaining": r.Remaining(),
		"reset_at":  r.NextResetAt(),
		"raw":       r,
	})
}

type kiroLoginStartBody struct {
	Label       string `json:"label"`
	RedirectURI string `json:"redirect_uri"` // typically http://localhost:3128
	ProxyURL    string `json:"proxy_url"`    // optional outbound proxy for token exchange + refresh
}

type kiroLoginStartResp struct {
	SignInURL   string `json:"signin_url"`
	State       string `json:"state"`
	RedirectURI string `json:"redirect_uri"`
}

func (h *Handler) handleKiroLoginStart(c *gin.Context) {
	var body kiroLoginStartBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	redirect := strings.TrimSpace(body.RedirectURI)
	if redirect == "" {
		redirect = "http://localhost:3128"
	}
	signin, state, err := h.kiro.PKCE().Start(redirect, body.Label, body.ProxyURL)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, kiroLoginStartResp{SignInURL: signin, State: state, RedirectURI: redirect})
}

type kiroLoginFinishBody struct {
	// Either supply the raw callback URL (preferred — mirrors the Claude
	// OAuth flow) or fill code/state/login_option explicitly.
	Callback    string `json:"callback"`
	Code        string `json:"code"`
	State       string `json:"state"`
	LoginOption string `json:"login_option"`
	Group       string `json:"group"`
}

func (h *Handler) handleKiroLoginFinish(c *gin.Context) {
	var body kiroLoginFinishBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	code := strings.TrimSpace(body.Code)
	state := strings.TrimSpace(body.State)
	loginOption := strings.TrimSpace(body.LoginOption)
	if cb := strings.TrimSpace(body.Callback); cb != "" && code == "" {
		pc, ps, plo, err := kirocreds.ParseKiroCallback(cb)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		code = pc
		if state == "" {
			state = ps
		}
		if loginOption == "" {
			loginOption = plo
		}
	}
	if code == "" || state == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing code/state"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	cred, label, err := h.kiro.PKCE().Finish(ctx, code, state, loginOption)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	entry, err := h.kiro.Store().Add(label, cred)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if grp := strings.TrimSpace(body.Group); grp != "" {
		if updated, err := h.kiro.Store().Update(entry.ID, nil, &grp, nil); err == nil {
			entry = updated
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"status":      "ok",
		"id":          entry.ID,
		"label":       entry.Label,
		"profile_arn": entry.Cred.ProfileARN,
	})
}
