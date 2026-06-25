package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// WorkspaceRoutes registers the platform-admin enterprise-workspace endpoints.
// Mounted under the RequireAdmin group, so every route here is operator-only —
// customers never provision their own workspace (by product decision).
func (h *Handler) WorkspaceRoutes(g *gin.RouterGroup) {
	g.GET("/workspaces", h.listWorkspaces)
	g.POST("/workspaces", h.createWorkspace)
	g.PATCH("/workspaces/:id", h.updateWorkspace)
	g.POST("/workspaces/:id/balance", h.adjustWorkspaceBalance)
	g.GET("/workspaces/:id/members", h.listWorkspaceMembers)
	g.POST("/workspaces/:id/members", h.addWorkspaceMember)
	g.DELETE("/workspaces/:id/members/:uid", h.removeWorkspaceMember)
}

func workspaceJSON(w *db.WorkspaceWithMeta) gin.H {
	return gin.H{
		"id": w.ID, "name": w.Name, "type": w.Type,
		"balance_usd": w.BalanceUSD, "daily_usd_cap": w.DailyUSDCap,
		"monthly_usd_cap": w.MonthlyUSDCap, "group_id": w.GroupID,
		"created_by": w.CreatedBy, "disabled": w.Disabled,
		"member_count": w.MemberCount, "created_at": w.CreatedAt.Unix(),
	}
}

func (h *Handler) listWorkspaces(c *gin.Context) {
	limit, offset := pageParams(c)
	list, total, err := h.DB.ListWorkspaces(c.Request.Context(), c.Query("type"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, w := range list {
		out = append(out, workspaceJSON(w))
	}
	c.JSON(http.StatusOK, gin.H{"workspaces": out, "total": total})
}

type createWorkspaceReq struct {
	Name          string  `json:"name"`
	BalanceUSD    float64 `json:"balance_usd"`
	DailyUSDCap   float64 `json:"daily_usd_cap"`
	MonthlyUSDCap float64 `json:"monthly_usd_cap"`
	GroupID       int64   `json:"group_id"`
	AdminEmail    string  `json:"admin_email"` // optional: designate the first space admin
}

func (h *Handler) createWorkspace(c *gin.Context) {
	var req createWorkspaceReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name required"})
		return
	}
	ctx := c.Request.Context()
	op := saasauth.CurrentUser(c)
	groupID := req.GroupID
	if groupID == 0 {
		if g, err := h.DB.DefaultGroup(ctx); err == nil {
			groupID = g.ID
		}
	}
	// Create with 0 balance, then book the initial grant as a ledger adjustment
	// so the provisioning shows up in the audit trail.
	ws, err := h.DB.CreateEnterpriseWorkspace(ctx, req.Name, 0, req.DailyUSDCap, req.MonthlyUSDCap, groupID, op.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if req.BalanceUSD > 0 {
		if _, err := h.DB.AdjustWorkspaceBalance(ctx, ws.ID, op.ID, req.BalanceUSD,
			"admin="+strconv.FormatInt(op.ID, 10), "initial provisioning"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	// Optionally designate the first space admin by email.
	if email := strings.TrimSpace(req.AdminEmail); email != "" {
		u, err := h.DB.GetUserByEmail(ctx, email)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "admin_email not a registered user"})
			return
		}
		if err := h.DB.UpsertWorkspaceMember(ctx, ws.ID, u.ID, db.WSRoleAdmin); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": ws.ID})
}

type updateWorkspaceReq struct {
	Name          *string  `json:"name"`
	DailyUSDCap   *float64 `json:"daily_usd_cap"`
	MonthlyUSDCap *float64 `json:"monthly_usd_cap"`
	GroupID       *int64   `json:"group_id"`
	Disabled      *bool    `json:"disabled"`
}

func (h *Handler) updateWorkspace(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var req updateWorkspaceReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if err := h.DB.UpdateWorkspace(c.Request.Context(), id, db.WorkspaceUpdate{
		Name: req.Name, DailyUSDCap: req.DailyUSDCap, MonthlyUSDCap: req.MonthlyUSDCap,
		GroupID: req.GroupID, Disabled: req.Disabled,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) adjustWorkspaceBalance(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var req balanceReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	op := saasauth.CurrentUser(c)
	bal, err := h.DB.AdjustWorkspaceBalance(c.Request.Context(), id, op.ID, req.DeltaUSD,
		"admin="+strconv.FormatInt(op.ID, 10), req.Note)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"new_balance_usd": bal})
}

func (h *Handler) listWorkspaceMembers(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	members, err := h.DB.ListWorkspaceMembers(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(members))
	for _, m := range members {
		out = append(out, gin.H{
			"user_id": m.UserID, "email": m.Email, "role": m.Role,
			"monthly_usd_cap": m.MonthlyUSDCap, "created_at": m.CreatedAt.Unix(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"members": out})
}

type addMemberReq struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *Handler) addWorkspaceMember(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var req addMemberReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	ctx := c.Request.Context()
	u, err := h.DB.GetUserByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "email not a registered user"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.DB.UpsertWorkspaceMember(ctx, id, u.ID, req.Role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user_id": u.ID})
}

func (h *Handler) removeWorkspaceMember(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad uid"})
		return
	}
	if err := h.DB.RemoveWorkspaceMember(c.Request.Context(), id, uid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
