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

	// pricing groups removed — billing multiplier is now a per-workspace
	// property (see admin WorkspaceRoutes). The pricing_groups table is frozen
	// (kept only for DefaultGroup plumbing + the users.group_id FK).

	// orders / payments
	g.GET("/orders", h.listOrders)
	g.POST("/orders/:id/reconcile", h.reconcileOrder)
	// balance adjustments / signup bonuses (money that entered wallets
	// without a payment order)
	g.GET("/adjustments", h.listAdjustments)

	// model health
	g.GET("/health", h.listHealth)
	g.POST("/health/refresh", h.refreshHealth)

	// admin business dashboard rollup (users + revenue + payments + chart)
	g.GET("/dashboard", h.dashboard)
}

func (h *Handler) dashboard(c *gin.Context) {
	snap, err := h.DB.AdminDashboard(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	users := gin.H{
		"total":    snap.UsersTotal,
		"verified": snap.UsersVerified,
		"new_30d":  snap.UsersNew30d,
		"disabled": snap.UsersDisabled,
	}
	revenue := gin.H{
		"topups_lifetime":  snap.TopupsLifetime,
		"topups_30d":       snap.Topups30d,
		"topups_7d":        snap.Topups7d,
		"charges_lifetime": snap.ChargesLifetime,
		"charges_30d":      snap.Charges30d,
		"charges_7d":       snap.Charges7d,
	}
	balance := gin.H{
		"outstanding":          snap.BalanceOutstanding,
		"orders_pending":       snap.OrdersPending,
		"orders_paid_lifetime": snap.OrdersPaidLifetime,
	}
	daily := make([]gin.H, 0, len(snap.DailyRevenue14d))
	for _, d := range snap.DailyRevenue14d {
		daily = append(daily, gin.H{"day": d.Day, "amount": d.Amount})
	}
	top := make([]gin.H, 0, len(snap.TopSpenders))
	for _, u := range snap.TopSpenders {
		top = append(top, gin.H{"user_id": u.UserID, "email": u.Email, "spent": u.Spent})
	}
	rec := make([]gin.H, 0, len(snap.RecentTopups))
	for _, o := range snap.RecentTopups {
		var paid int64
		if !o.PaidAt.IsZero() {
			paid = o.PaidAt.Unix()
		}
		rec = append(rec, gin.H{
			"out_trade_no": o.OutTradeNo,
			"user_id":      o.UserID,
			"user_email":   o.UserEmail,
			"cny_amount":   o.CNYAmount,
			"usd_credit":   o.USDCredit,
			"paid_at":      paid,
		})
	}
	signups := make([]gin.H, 0, len(snap.RecentSignups))
	for _, u := range snap.RecentSignups {
		signups = append(signups, gin.H{
			"id":         u.ID,
			"email":      u.Email,
			"role":       u.Role,
			"verified":   u.Verified,
			"disabled":   u.Disabled,
			"created_at": u.CreatedAt.Unix(),
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"users":          users,
		"revenue":        revenue,
		"balance":        balance,
		"daily_revenue":  daily,
		"top_spenders":   top,
		"recent_topups":  rec,
		"recent_signups": signups,
	})
}

// WalletTotalsHandler is a stand-alone handler exposing the fleet-wide
// wallet rollup. Mounted on the user-authed group (not RequireAdmin)
// because the operator console is visible to any signed-in user — the
// rollup is an aggregate sum, no per-user identity exposed.
func (h *Handler) WalletTotalsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		t, err := h.DB.FleetTotals(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"user_paid_usd": t.UserPaidUSD,
			"topups_usd":    t.TopupsUSD,
			"charge_count":  t.ChargeCount,
		})
	}
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

// pageParams parses limit/offset query params for list endpoints, clamping
// limit to [1, 200] (default 50) and offset to >= 0.
func pageParams(c *gin.Context) (limit, offset int) {
	limit, _ = strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset, _ = strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (h *Handler) userTx(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	limit, offset := pageParams(c)
	// nil kinds → the full per-user ledger (charges included), unlike the
	// user-facing billing history which hides charges.
	txs, total, err := h.DB.ListWalletTx(c.Request.Context(), id, nil, limit, offset)
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
	c.JSON(http.StatusOK, gin.H{"transactions": out, "total": total})
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
	limit, offset := pageParams(c)
	os, total, err := h.DB.ListAllOrders(c.Request.Context(), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"orders": os, "total": total})
}

// listAdjustments returns the fleet-wide list of wallet adjustments — manual
// operator grants and channel signup bonuses (kind='adjust') — that credited
// wallets without a payment order. The frontend tells the two apart by `ref`
// (signup bonuses carry "signup_bonus:<slug>").
func (h *Handler) listAdjustments(c *gin.Context) {
	limit, offset := pageParams(c)
	adj, total, err := h.DB.ListFleetAdjustments(c.Request.Context(), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(adj))
	for _, a := range adj {
		out = append(out, gin.H{
			"id": a.ID, "user_id": a.UserID, "email": a.Email,
			"amount_usd": a.AmountUSD, "ref": a.Ref, "note": a.Note,
			"created_at": a.CreatedAt.Unix(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"adjustments": out, "total": total})
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
	displayName := func(authID, provider, _ string) string {
		// Infer kind from auth_id prefix heuristic — API key files start with
		// "apikey-" or contain "_api_key"; OAuth files contain "oauth" or start with
		// "sk-ant-oat" after redaction. We use the model probe as a proxy:
		// if the auth_id looks like a filename with "apikey" it's apikey, else oauth.
		kind := "oauth"
		lower := authID
		switch {
		case len(lower) > 7 && lower[:7] == "apikey-":
			kind = "api"
		case len(lower) > 10 && lower[:10] == "openai_api":
			kind = "api"
		case len(lower) > 7 && lower[:7] == "openai-":
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
