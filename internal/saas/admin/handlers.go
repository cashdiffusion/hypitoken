// Package admin provides operator-only endpoints under /api/v2/admin/*.
// All routes require role=admin.
package admin

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

type Handler struct {
	DB *db.DB
}

func New(store *db.DB) *Handler { return &Handler{DB: store} }

func (h *Handler) Routes(g *gin.RouterGroup) {
	// users
	g.GET("/users", h.listUsers)
	g.PATCH("/users/:id", h.updateUser)
	g.POST("/users/:id/balance", h.adjustBalance)
	g.GET("/users/:id/transactions", h.userTx)

	// pricing groups
	g.GET("/groups", h.listGroups)
	g.POST("/groups", h.createGroup)
	g.PATCH("/groups/:id", h.updateGroup)
	g.DELETE("/groups/:id", h.deleteGroup)

	// orders / payments
	g.GET("/orders", h.listOrders)
	g.POST("/orders/:id/reconcile", h.reconcileOrder)

	// model health
	g.GET("/health", h.listHealth)
	g.POST("/health/refresh", h.refreshHealth)
}

func (h *Handler) listUsers(c *gin.Context) {
	q := c.Query("q")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	users, total, err := h.DB.ListUsers(c.Request.Context(), q, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(users))
	for _, u := range users {
		out = append(out, gin.H{
			"id": u.ID, "email": u.Email, "role": u.Role,
			"balance_usd": u.BalanceUSD, "group_id": u.GroupID,
			"email_verified": u.EmailVerified, "disabled": u.Disabled,
			"created_at": u.CreatedAt.Unix(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"users": out, "total": total})
}

type updateUserReq struct {
	Role     *string `json:"role"`
	GroupID  *int64  `json:"group_id"`
	Disabled *bool   `json:"disabled"`
}

func (h *Handler) updateUser(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var req updateUserReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	ctx := c.Request.Context()
	if req.Role != nil {
		_ = h.DB.SetUserRole(ctx, id, *req.Role)
	}
	if req.GroupID != nil {
		_ = h.DB.SetUserGroup(ctx, id, *req.GroupID)
	}
	if req.Disabled != nil {
		_ = h.DB.SetUserDisabled(ctx, id, *req.Disabled)
	}
	u, err := h.DB.GetUser(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, u)
}

type balanceReq struct {
	DeltaUSD float64 `json:"delta_usd"`
	Note     string  `json:"note"`
}

func (h *Handler) adjustBalance(c *gin.Context) {
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
	bal, err := h.DB.AddBalance(c.Request.Context(), id, db.TxKindAdjust, req.DeltaUSD,
		"admin="+strconv.FormatInt(op.ID, 10), req.Note, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"new_balance_usd": bal})
}

func (h *Handler) userTx(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	txs, err := h.DB.ListWalletTx(c.Request.Context(), id, 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(txs))
	for _, t := range txs {
		out = append(out, gin.H{
			"id": t.ID, "kind": t.Kind, "amount_usd": t.AmountUSD,
			"ref": t.Ref, "note": t.Note, "created_at": t.CreatedAt.Unix(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"transactions": out})
}

func (h *Handler) listGroups(c *gin.Context) {
	gs, err := h.DB.ListGroups(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": gs})
}

// groupReq is the JSON shape for creating/updating a pricing group.
// Billing semantics: final_charge_USD = official_USD × multiplier.
// Defaults (when nothing is sent): claude=0.3, codex=0.05.
type groupReq struct {
	Name             string  `json:"name"`
	Description      string  `json:"description"`
	CodexMultiplier  float64 `json:"codex_multiplier"`
	ClaudeMultiplier float64 `json:"claude_multiplier"`
	CredentialGroup  string  `json:"credential_group"`
}

func (r groupReq) toParams() db.GroupParams {
	return db.GroupParams{
		Name:             r.Name,
		Description:      r.Description,
		CodexMultiplier:  r.CodexMultiplier,
		ClaudeMultiplier: r.ClaudeMultiplier,
		CredentialGroup:  r.CredentialGroup,
	}
}

func (h *Handler) createGroup(c *gin.Context) {
	var req groupReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	g, err := h.DB.CreateGroup(c.Request.Context(), req.toParams())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, g)
}

func (h *Handler) updateGroup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	var req groupReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	g, err := h.DB.UpdateGroup(c.Request.Context(), id, req.toParams())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, g)
}

func (h *Handler) deleteGroup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	if err := h.DB.DeleteGroup(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// Reconciler is set by main.go after the billing handler is constructed.
// Decouples this admin package from internal/saas/billing (which would
// otherwise re-introduce an import cycle through the gateway interface).
var Reconciler func(ctx interface{ Done() <-chan struct{} }, outTradeNo string) (string, error)

// reconcileOrder asks Alipay for the authoritative state of an order and
// applies the result through the same validation funnel the async notify
// uses. Use case: a notify never arrived, but the user did pay — repair
// without manual SQL.
func (h *Handler) reconcileOrder(c *gin.Context) {
	if Reconciler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "reconciler not configured"})
		return
	}
	out := c.Param("id")
	state, err := Reconciler(c.Request.Context(), out)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"out_trade_no": out, "state": state})
}

func (h *Handler) listOrders(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	os, err := h.DB.ListAllOrders(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"orders": os})
}

func (h *Handler) listHealth(c *gin.Context) {
	hs, err := h.DB.ListModelHealth(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Build sequential display names per provider+kind.
	// Counters keyed by "<provider>-<kind>" (e.g. "anthropic-oauth", "openai-apikey").
	counters := map[string]int{}
	displayName := func(authID, provider, model string) string {
		// Infer kind from auth_id prefix heuristic — API key files start with
		// "apikey-" or contain "_api_key"; OAuth files contain "oauth" or start with
		// "sk-ant-oat" after redaction. We use the model probe as a proxy:
		// if the auth_id looks like a filename with "apikey" it's apikey, else oauth.
		kind := "oauth"
		lower := authID
		if len(lower) > 7 && lower[:7] == "apikey-" {
			kind = "api"
		} else if len(lower) > 10 && lower[:10] == "openai_api" {
			kind = "api"
		} else if len(lower) > 7 && lower[:7] == "openai-" {
			kind = "api"
		}
		// Shorten provider name for display.
		prov := "claude"
		if provider == "openai" {
			prov = "codex"
		}
		key := prov + "-" + kind + "-" + authID // stable key per credential
		if _, seen := counters[key]; !seen {
			grpKey := prov + "-" + kind
			counters[grpKey]++
			counters[key] = counters[grpKey]
		}
		return fmt.Sprintf("%s-%s-%03d", prov, kind, counters[key])
	}
	store := h.DB
	out := make([]gin.H, 0, len(hs))
	for _, rec := range hs {
		name := displayName(rec.AuthID, rec.Provider, rec.Model)
		hist, _ := store.ListModelHealthHistory(c.Request.Context(), rec.AuthID, rec.Model, 90)
		histSlice := make([]gin.H, 0, len(hist))
		for _, r := range hist {
			histSlice = append(histSlice, gin.H{
				"status": r.Status, "latency_ms": r.LatencyMs, "checked_at": r.CheckedAt.Unix(),
			})
		}
		out = append(out, gin.H{
			"id": rec.ID, "display_name": name, "provider": rec.Provider,
			"status": rec.Status, "latency_ms": rec.LatencyMs, "error": rec.Error,
			"checked_at": rec.CheckedAt.Unix(), "history": histSlice,
		})
	}
	c.JSON(http.StatusOK, gin.H{"checks": out, "as_of": time.Now().Unix()})
}

// HealthRefresher is set by main.go after the model health checker is wired.
var HealthRefresher func()

func (h *Handler) refreshHealth(c *gin.Context) {
	if HealthRefresher == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "health refresher not configured"})
		return
	}
	go HealthRefresher()
	c.JSON(http.StatusOK, gin.H{"refreshing": true})
}
