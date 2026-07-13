// Package workspace serves the space-admin team-management endpoints under
// /api/v2/workspaces/:id/* (gated by RequireWorkspaceAdmin) plus the invite
// accept flow under /api/v2/invites/*. A space admin is a CUSTOMER, so these
// endpoints expose ONLY their own workspace's member usage — never upstream
// credentials, fleet state, other workspaces, or system status.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/mail"
)

type Handler struct {
	DB        *db.DB
	Mailer    mail.Mailer
	SiteURL   string
	SiteName  string
	InviteTTL time.Duration
}

func New(store *db.DB, mailer mail.Mailer, siteURL, siteName string) *Handler {
	return &Handler{
		DB:        store,
		Mailer:    mailer,
		SiteURL:   strings.TrimRight(strings.TrimSpace(siteURL), "/"),
		SiteName:  siteName,
		InviteTTL: 7 * 24 * time.Hour,
	}
}

// TeamRoutes registers the per-workspace admin endpoints on a group already
// rooted at /workspaces/:id and wrapped with RequireWorkspaceAdmin, so only the
// space's own admins (or a platform admin) reach them.
func (h *Handler) TeamRoutes(g *gin.RouterGroup) {
	g.GET("", h.getWorkspace)
	g.GET("/usage", h.usage)
	g.GET("/ledger", h.ledger)
	g.GET("/members", h.listMembers)
	g.PATCH("/members/:uid", h.updateMember)
	g.DELETE("/members/:uid", h.removeMember)
	g.GET("/members/:uid/tokens", h.listMemberTokens)
	g.PATCH("/members/:uid/tokens/:tid", h.setTokenCap)
	g.GET("/invites", h.listInvites)
	g.POST("/invites", h.createInvite)
	g.DELETE("/invites/:iid", h.revokeInvite)
	g.POST("/invites/:iid/resend", h.resendInvite)
}

// AcceptRoutes registers the invite-accept endpoints. The caller wraps this with
// RequireUser — any signed-in user can accept an invite addressed to their email.
func (h *Handler) AcceptRoutes(g *gin.RouterGroup) {
	g.GET("/invites/:token", h.inviteInfo)
	g.POST("/invites/:token/accept", h.acceptInvite)
}

func wsID(c *gin.Context) int64 {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	return id
}

func monthStart() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
}

// maskToken hides the secret body of a key, keeping only the recognizable
// prefix + last 4 chars — enough for a space admin to identify a member's key
// without ever seeing the credential.
func maskToken(tok string) string {
	if len(tok) <= 12 {
		return "sk-cpa-…"
	}
	return tok[:7] + "…" + tok[len(tok)-4:]
}

// maskEmail returns a privacy-preserving form like "a***@corp.com" so a landing
// page can hint which account an invite is for without exposing the full address.
func maskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return "***"
	}
	local, domain := email[:at], email[at:]
	if len(local) <= 1 {
		return "*" + domain
	}
	return local[:1] + strings.Repeat("*", 3) + domain
}

func (h *Handler) getWorkspace(c *gin.Context) {
	w, err := h.DB.GetWorkspace(c.Request.Context(), wsID(c))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id": w.ID, "name": w.Name, "type": w.Type, "balance_usd": w.BalanceUSD,
		"daily_usd_cap": w.DailyUSDCap, "monthly_usd_cap": w.MonthlyUSDCap,
		"disabled": w.Disabled,
	})
}

func (h *Handler) usage(c *gin.Context) {
	ctx := c.Request.Context()
	id := wsID(c)
	w, err := h.DB.GetWorkspace(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	members, err := h.DB.WorkspaceMemberUsage(ctx, id, monthStart())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	mOut := make([]gin.H, 0, len(members))
	for _, m := range members {
		mOut = append(mOut, gin.H{
			"user_id": m.UserID, "email": m.Email, "role": m.Role,
			"monthly_usd_cap": m.MonthlyUSDCap, "spent_usd": m.SpentUSD,
		})
	}
	spentMonth, _ := h.DB.SumChargeSinceForWorkspace(ctx, id, monthStart())
	cm, xm := w.ClaudeMultiplier, w.CodexMultiplier
	if cm <= 0 {
		cm = 0.3
	}
	if xm <= 0 {
		xm = 0.05
	}
	c.JSON(http.StatusOK, gin.H{
		"balance_usd":       w.BalanceUSD,
		"daily_usd_cap":     w.DailyUSDCap,
		"monthly_usd_cap":   w.MonthlyUSDCap,
		"spent_month_usd":   spentMonth,
		"claude_multiplier": cm,
		"codex_multiplier":  xm,
		"members":           mOut,
	})
}

func (h *Handler) ledger(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	rows, total, err := h.DB.WorkspaceLedger(c.Request.Context(), wsID(c), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, gin.H{
			"user_id": r.UserID, "email": r.Email, "ref": r.Ref,
			"amount_usd": r.AmountUSD, "created_at": r.CreatedAt.Unix(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"entries": out, "total": total})
}

func (h *Handler) listMembers(c *gin.Context) {
	members, err := h.DB.ListWorkspaceMembers(c.Request.Context(), wsID(c))
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

type updateMemberReq struct {
	MonthlyUSDCap *float64 `json:"monthly_usd_cap"`
	Role          *string  `json:"role"`
}

func (h *Handler) updateMember(c *gin.Context) {
	id := wsID(c)
	uid, _ := strconv.ParseInt(c.Param("uid"), 10, 64)
	var req updateMemberReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	ctx := c.Request.Context()
	if req.MonthlyUSDCap != nil {
		if err := h.DB.SetMemberMonthlyCap(ctx, id, uid, *req.MonthlyUSDCap); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if req.Role != nil {
		if err := h.DB.UpsertWorkspaceMember(ctx, id, uid, *req.Role); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) removeMember(c *gin.Context) {
	id := wsID(c)
	uid, _ := strconv.ParseInt(c.Param("uid"), 10, 64)
	op := saasauth.CurrentUser(c)
	if op != nil && op.ID == uid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot remove yourself"})
		return
	}
	if err := h.DB.RemoveWorkspaceMember(c.Request.Context(), id, uid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) listMemberTokens(c *gin.Context) {
	id := wsID(c)
	uid, _ := strconv.ParseInt(c.Param("uid"), 10, 64)
	toks, err := h.DB.ListWorkspaceMemberTokens(c.Request.Context(), id, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(toks))
	for _, t := range toks {
		tags := t.Tags
		if tags == nil {
			tags = []string{}
		}
		out = append(out, gin.H{
			"id": t.ID, "name": t.Name, "token_masked": maskToken(t.Token),
			"monthly_usd_cap": t.MonthlyUSDCap, "admin_monthly_cap": t.AdminMonthlyCap,
			"disabled": t.Disabled, "created_at": t.CreatedAt.Unix(),
			"tags": tags,
		})
	}
	c.JSON(http.StatusOK, gin.H{"tokens": out})
}

// setTokenCapReq carries both space-admin-editable fields on a member's key. Tags
// is a pointer so an omitted field means "leave the labels alone", distinct from
// an explicit [] meaning "clear them".
type setTokenCapReq struct {
	AdminMonthlyCap float64   `json:"admin_monthly_cap"`
	Tags            *[]string `json:"tags"`
}

func (h *Handler) setTokenCap(c *gin.Context) {
	id := wsID(c)
	tid, _ := strconv.ParseInt(c.Param("tid"), 10, 64)
	var req setTokenCapReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	// Both writes are workspace-scoped at the SQL level, so a space admin can
	// never reach a key bound to someone's personal space.
	if err := h.DB.SetTokenAdminMonthlyCap(c.Request.Context(), tid, id, req.AdminMonthlyCap); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "token not in this workspace"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if req.Tags != nil {
		if err := h.DB.SetTokenTags(c.Request.Context(), tid, id, *req.Tags); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "token not in this workspace"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) listInvites(c *gin.Context) {
	invites, err := h.DB.ListWorkspaceInvites(c.Request.Context(), wsID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(invites))
	for _, i := range invites {
		out = append(out, gin.H{
			"id": i.ID, "email": i.Email, "role": i.Role,
			"status": i.EffectiveStatus(), "expires_at": i.ExpiresAt.Unix(),
			"created_at": i.CreatedAt.Unix(), "link": h.inviteLink(i.Token),
		})
	}
	c.JSON(http.StatusOK, gin.H{"invites": out})
}

type createInviteReq struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h *Handler) createInvite(c *gin.Context) {
	id := wsID(c)
	var req createInviteReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || !strings.Contains(email, "@") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "valid email required"})
		return
	}
	ctx := c.Request.Context()
	// Already a member? Nothing to do.
	if u, err := h.DB.GetUserByEmail(ctx, email); err == nil {
		if _, merr := h.DB.GetWorkspaceMember(ctx, id, u.ID); merr == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "already a member"})
			return
		}
	}
	inv, err := h.DB.CreateWorkspaceInvite(ctx, id, email, req.Role, currentUserID(c), h.InviteTTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.sendInviteEmail(ctx, id, inv)
	c.JSON(http.StatusOK, gin.H{
		"id": inv.ID, "email": inv.Email, "role": inv.Role,
		"status": inv.EffectiveStatus(), "link": h.inviteLink(inv.Token),
	})
}

func (h *Handler) revokeInvite(c *gin.Context) {
	iid, _ := strconv.ParseInt(c.Param("iid"), 10, 64)
	if err := h.DB.RevokeWorkspaceInvite(c.Request.Context(), iid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) resendInvite(c *gin.Context) {
	id := wsID(c)
	iid, _ := strconv.ParseInt(c.Param("iid"), 10, 64)
	ctx := c.Request.Context()
	inv, err := h.DB.RefreshWorkspaceInvite(ctx, iid, h.InviteTTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.sendInviteEmail(ctx, id, inv)
	c.JSON(http.StatusOK, gin.H{"ok": true, "link": h.inviteLink(inv.Token)})
}

// --- invite accept (RequireUser) ---

func (h *Handler) inviteInfo(c *gin.Context) {
	inv, err := h.DB.GetWorkspaceInviteByToken(c.Request.Context(), c.Param("token"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invite not found"})
		return
	}
	wsName := ""
	if w, werr := h.DB.GetWorkspace(c.Request.Context(), inv.WorkspaceID); werr == nil {
		wsName = w.Name
	}
	// Do NOT leak the raw invitee email to whoever holds the token — return a
	// masked form plus whether it's addressed to the *current* signed-in user,
	// so the landing page can say "accept" or "wrong account" without exposing
	// PII or enabling email enumeration.
	forYou := false
	if u := saasauth.CurrentUser(c); u != nil {
		forYou = strings.EqualFold(u.Email, inv.Email)
	}
	c.JSON(http.StatusOK, gin.H{
		"workspace_id": inv.WorkspaceID, "workspace_name": wsName,
		"email_masked": maskEmail(inv.Email), "for_you": forYou,
		"role": inv.Role, "status": inv.EffectiveStatus(),
	})
}

func (h *Handler) acceptInvite(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "login required"})
		return
	}
	ctx := c.Request.Context()
	inv, err := h.DB.GetWorkspaceInviteByToken(ctx, c.Param("token"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invite not found"})
		return
	}
	if inv.EffectiveStatus() != db.InvitePending {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invite is no longer valid", "status": inv.EffectiveStatus()})
		return
	}
	if !u.EmailVerified {
		c.JSON(http.StatusForbidden, gin.H{"error": "verify your email before accepting an invite"})
		return
	}
	if !strings.EqualFold(inv.Email, u.Email) {
		c.JSON(http.StatusForbidden, gin.H{"error": "this invite was sent to a different email"})
		return
	}
	if err := h.DB.AcceptWorkspaceInvite(ctx, inv, u.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "workspace_id": inv.WorkspaceID})
}

// --- helpers ---

func (h *Handler) inviteLink(token string) string {
	return h.SiteURL + "/join/" + token
}

// sendInviteEmail best-effort emails the invite link. A nil mailer (or a send
// failure) is non-fatal — the space admin can always copy the link from the UI.
func (h *Handler) sendInviteEmail(ctx context.Context, workspaceID int64, inv *db.WorkspaceInvite) {
	if h.Mailer == nil {
		return
	}
	wsName := "a workspace"
	if w, err := h.DB.GetWorkspace(ctx, workspaceID); err == nil && w.Name != "" {
		wsName = w.Name
	}
	link := h.inviteLink(inv.Token)
	subject := fmt.Sprintf("You're invited to %s on %s", wsName, h.SiteName)
	html := fmt.Sprintf(
		`<p>You've been invited to join <strong>%s</strong> on %s.</p>`+
			`<p><a href="%s">Accept the invitation</a> (expires in 7 days).</p>`+
			`<p>If the link doesn't work, paste this into your browser:<br>%s</p>`,
		wsName, h.SiteName, link, link)
	text := fmt.Sprintf("You've been invited to join %s on %s.\nAccept: %s\n(The link expires in 7 days.)", wsName, h.SiteName, link)
	go func() { _ = h.Mailer.Send(inv.Email, subject, html, text) }()
}

func currentUserID(c *gin.Context) int64 {
	if u := saasauth.CurrentUser(c); u != nil {
		return u.ID
	}
	return 0
}
