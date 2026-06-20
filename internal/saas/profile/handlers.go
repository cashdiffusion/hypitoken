// Package profile provides /api/v2/me/profile (public nickname + leaderboard
// opt-in) and /api/v2/me/greeting (coarse IP geolocation for the dashboard's
// personalised welcome line). Both are authed user routes.
package profile

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

type Handler struct {
	DB *db.DB
}

func New(store *db.DB) *Handler {
	return &Handler{DB: store}
}

// Routes mounts the profile + greeting endpoints under the authed group.
func (h *Handler) Routes(g *gin.RouterGroup) {
	g.GET("/me/profile", h.getProfile)
	g.PATCH("/me/profile", h.patchProfile)
	g.GET("/me/greeting", h.greeting)
}

type profileView struct {
	DisplayName      string `json:"display_name"`
	NameIsDefault    bool   `json:"name_is_default"`
	PublicOptIn      bool   `json:"public_opt_in"`
	LifetimeTokens   int64  `json:"lifetime_tokens"`
	LifetimeRequests int64  `json:"lifetime_requests"`
	LastActiveAt     int64  `json:"last_active_at"`
}

func toView(p *db.UserProfile) profileView {
	v := profileView{
		DisplayName:      p.DisplayName,
		NameIsDefault:    p.NameIsDefault,
		PublicOptIn:      p.PublicOptIn,
		LifetimeTokens:   p.LifetimeTokens,
		LifetimeRequests: p.LifetimeRequests,
	}
	if !p.LastActiveAt.IsZero() {
		v.LastActiveAt = p.LastActiveAt.Unix()
	}
	return v
}

func (h *Handler) getProfile(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	p, err := h.DB.GetOrCreateProfile(c.Request.Context(), u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toView(p))
}

type patchReq struct {
	DisplayName *string `json:"display_name"`
	PublicOptIn *bool   `json:"public_opt_in"`
}

// nickname rules: 2–24 visible chars after trim; letters/digits/space and a
// small punctuation set; no control chars. Kept permissive enough for CJK.
func validateNickname(s string) (string, bool) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n < 2 || n > 24 {
		return "", false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return s, true
}

func (h *Handler) patchProfile(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	var req patchReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	var namePtr *string
	if req.DisplayName != nil {
		clean, ok := validateNickname(*req.DisplayName)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid nickname (2-24 chars)"})
			return
		}
		namePtr = &clean
	}
	p, err := h.DB.UpdateProfile(c.Request.Context(), u.ID, namePtr, req.PublicOptIn)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toView(p))
}

// ---- greeting / IP geolocation (fully offline) ------------------------------
//
// Deliberately header-only: no external geo API call (the deployment is offline
// / no-CDN by policy). In production hypitoken sits behind Cloudflare Tunnel +
// Caddy, which inject CF-IPCountry (always, free tier) and CF-IPCity (only on
// the enterprise plan). We surface whatever is present and degrade gracefully —
// the frontend localises the 2-letter country code with the browser-native,
// offline Intl.DisplayNames, and the time-of-day is computed client-side. A
// reverse proxy can also feed X-Country / X-City explicitly.

type greetView struct {
	CountryCode string `json:"country_code"` // ISO-3166 alpha-2, may be empty
	City        string `json:"city"`         // best-effort, may be empty
}

func (h *Handler) greeting(c *gin.Context) {
	if saasauth.CurrentUser(c) == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	country := strings.ToUpper(firstNonEmpty(c.GetHeader("CF-IPCountry"), c.GetHeader("X-Country")))
	city := firstNonEmpty(c.GetHeader("CF-IPCity"), c.GetHeader("X-City"))
	// CF placeholders for unknown / Tor / internal — treat as no signal.
	switch country {
	case "XX", "T1", "":
		country = ""
	}
	if len(country) != 2 {
		country = ""
	}
	c.JSON(http.StatusOK, greetView{CountryCode: country, City: city})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
