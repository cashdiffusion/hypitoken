// Package tokens provides /api/v2/tokens — per-user token CRUD.
package tokens

import (
	"net/http"

	"github.com/gin-gonic/gin"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

type Handler struct {
	DB *db.DB
}

func New(store *db.DB) *Handler { return &Handler{DB: store} }

func (h *Handler) Routes(g *gin.RouterGroup) {
	g.GET("", h.list)
	g.POST("", h.create)
	g.PATCH("/:id", h.update)
	g.POST("/:id/rotate", h.rotate)
	g.DELETE("/:id", h.del)
}

type tokenView struct {
	ID              int64    `json:"id"`
	Token           string   `json:"token"`
	Name            string   `json:"name"`
	DailyUSDCap     float64  `json:"daily_usd_cap"`
	MonthlyUSDCap   float64  `json:"monthly_usd_cap"`
	MaxConcurrent   int      `json:"max_concurrent"`
	RPM             int      `json:"rpm"`
	Disabled        bool     `json:"disabled"`
	LastUsedAt      int64    `json:"last_used_at"`
	CreatedAt       int64    `json:"created_at"`
	Groups          []string `json:"groups,omitempty"`
	WorkspaceID     int64    `json:"workspace_id"`                // billing target
	AdminMonthlyCap float64  `json:"admin_monthly_cap,omitempty"` // space-admin imposed (read-only here)
}

func toView(t *db.UserToken) tokenView {
	return tokenView{
		ID: t.ID, Token: t.Token, Name: t.Name,
		DailyUSDCap: t.DailyUSDCap, MonthlyUSDCap: t.MonthlyUSDCap,
		MaxConcurrent: t.MaxConcurrent, RPM: t.RPM,
		Disabled:        t.Disabled,
		LastUsedAt:      t.LastUsedAt.Unix(),
		CreatedAt:       t.CreatedAt.Unix(),
		Groups:          append([]string(nil), t.Groups...),
		WorkspaceID:     t.WorkspaceID,
		AdminMonthlyCap: t.AdminMonthlyCap,
	}
}

func (h *Handler) list(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	ts, err := h.DB.ListUserTokens(c.Request.Context(), u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]tokenView, 0, len(ts))
	for _, t := range ts {
		out = append(out, toView(t))
	}
	c.JSON(http.StatusOK, gin.H{"tokens": out})
}

type createReq struct {
	Name          string   `json:"name"`
	DailyUSDCap   float64  `json:"daily_usd_cap"`
	MonthlyUSDCap float64  `json:"monthly_usd_cap"`
	MaxConcurrent int      `json:"max_concurrent"`
	RPM           int      `json:"rpm"`
	Groups        []string `json:"groups,omitempty"` // priority-ordered credential-group fallthrough
	WorkspaceID   int64    `json:"workspace_id"`     // billing target; 0 = personal workspace
}

func (h *Handler) create(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	var req createReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	// A key may bill any workspace the user is a member of (personal or an
	// enterprise space they belong to). Reject binding to a space they're not in.
	if req.WorkspaceID > 0 {
		if _, err := h.DB.GetWorkspaceMember(c.Request.Context(), req.WorkspaceID, u.ID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "not a member of that workspace"})
			return
		}
	}
	t, err := h.DB.CreateUserToken(c.Request.Context(), u.ID, db.TokenParams{
		Name: req.Name, DailyUSDCap: req.DailyUSDCap, MonthlyUSDCap: req.MonthlyUSDCap,
		MaxConcurrent: req.MaxConcurrent, RPM: req.RPM,
		Groups: req.Groups, WorkspaceID: req.WorkspaceID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toView(t))
}

func (h *Handler) update(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	id, err := parseID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	t, err := h.DB.GetUserToken(c.Request.Context(), id)
	if err != nil || t.UserID != u.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req struct {
		createReq
		Disabled *bool `json:"disabled"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if err := h.DB.UpdateUserToken(c.Request.Context(), id, db.TokenParams{
		Name: req.Name, DailyUSDCap: req.DailyUSDCap, MonthlyUSDCap: req.MonthlyUSDCap,
		MaxConcurrent: req.MaxConcurrent, RPM: req.RPM,
		Groups: req.Groups,
	}, req.Disabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	t, _ = h.DB.GetUserToken(c.Request.Context(), id)
	c.JSON(http.StatusOK, toView(t))
}

func (h *Handler) rotate(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	id, err := parseID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	t, err := h.DB.GetUserToken(c.Request.Context(), id)
	if err != nil || t.UserID != u.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	t, err = h.DB.RotateUserToken(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toView(t))
}

func (h *Handler) del(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	id, err := parseID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	t, err := h.DB.GetUserToken(c.Request.Context(), id)
	if err != nil || t.UserID != u.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err := h.DB.DeleteUserToken(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func parseID(c *gin.Context) (int64, error) {
	var id int64
	idStr := c.Param("id")
	_, err := parseInt(idStr, &id)
	return id, err
}

func parseInt(s string, out *int64) (int, error) {
	var n int64
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch < '0' || ch > '9' {
			return i, errBadInt
		}
		n = n*10 + int64(ch-'0')
	}
	*out = n
	return len(s), nil
}

type intErr string

func (e intErr) Error() string { return string(e) }

const errBadInt intErr = "bad int"
