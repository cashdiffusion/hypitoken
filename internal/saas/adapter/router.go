package adapter

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/cc-core/auth"
	legacyadmin "github.com/wjsoj/CPA-Claude/internal/admin"
	"github.com/wjsoj/CPA-Claude/internal/pricing"
	"github.com/wjsoj/CPA-Claude/internal/requestlog"
	"github.com/wjsoj/CPA-Claude/internal/saas/admin"
	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/tokens"
)

// Mount attaches all /api/v2/* SaaS routes onto engine. Public-routes (auth
// + billing notify) sit outside RequireUser. credH may be nil — when set,
// /api/v2/admin/credentials/* is exposed. legacyH may be nil — when set, the
// /api/v2/admin/* group also exposes request-log queries + Anthropic OAuth
// quota probe (handlers reused from the legacy /mgmt-console panel).
func Mount(engine *gin.Engine, store *db.DB, authH *saasauth.Handler, tokensH *tokens.Handler, billingH *billing.Handler, adminH *admin.Handler, credH *admin.CredHandler, iss *saasauth.Issuer, legacyH *legacyadmin.Handler, logDir string, catalog *pricing.Catalog) {
	v2 := engine.Group("/api/v2")

	// Public.
	v2.GET("/site", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	v2.GET("/exchange-rate", billingH.UserRateRouteShim())
	authG := v2.Group("/auth")
	authH.Routes(authG)
	billingH.PublicRoutes(v2)

	// Authenticated.
	authed := v2.Group("")
	authed.Use(saasauth.RequireUser(iss, store))
	authed.GET("/me", func(c *gin.Context) {
		u := saasauth.CurrentUser(c)
		if u == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}
		g, _ := store.GetGroup(c.Request.Context(), u.GroupID)
		c.JSON(http.StatusOK, gin.H{
			"user": gin.H{
				"id": u.ID, "email": u.Email, "role": u.Role,
				"balance_usd": u.BalanceUSD, "group_id": u.GroupID,
				"email_verified": u.EmailVerified, "created_at": u.CreatedAt.Unix(),
			},
			"group": g,
		})
	})
	tokensH.Routes(authed.Group("/tokens"))
	billingH.UserRoutes(authed.Group("/billing"))

	// Per-user request log. Same shape as /admin/api/requests but filtered
	// to records emitted while the requester was the authenticated user —
	// powers the /app/logs page where customers reconcile every charge
	// against the catalog rate. Read-only, no mutation surface.
	authed.GET("/me/requests", func(c *gin.Context) {
		u := saasauth.CurrentUser(c)
		if u == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		f := requestlog.Filter{
			Dir:    logDir,
			UserID: u.ID,
			Limit:  limit,
			Offset: offset,
		}
		// Optional secondary filters — useful for users wanting to drill
		// down to a single token or model.
		if mtok := c.Query("model"); mtok != "" {
			f.Model = mtok
		}
		if prov := c.Query("provider"); prov != "" {
			f.Provider = prov
		}
		if ct := c.Query("client_token"); ct != "" {
			f.ClientToken = ct
		}
		res, err := requestlog.Query(f)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Attach the price card for every distinct model that appears so
		// the frontend can render a per-row "how was this charged"
		// breakdown without a follow-up RPC.
		prices := map[string]gin.H{}
		seen := map[string]struct{}{}
		add := func(model string) {
			if model == "" {
				return
			}
			if _, ok := seen[model]; ok {
				return
			}
			seen[model] = struct{}{}
			p := catalog.Lookup(auth.ProviderAnthropic, model)
			// auth.ProviderAnthropic is the wrong assumption for codex-side
			// rows; lookup() falls back to provider-default → global default
			// so we still get a usable card for unknown models.
			prices[model] = gin.H{
				"input_per_1m":        p.InputPer1M,
				"output_per_1m":       p.OutputPer1M,
				"cache_read_per_1m":   p.CacheReadPer1M,
				"cache_create_per_1m": p.CacheCreatePer1M,
			}
		}
		for _, e := range res.Entries {
			add(e.Model)
		}
		for m := range res.ByModel {
			add(m)
		}
		c.JSON(http.StatusOK, gin.H{
			"summary":  res.Summary,
			"by_model": res.ByModel,
			"entries":  res.Entries,
			"scanned":  res.Scanned,
			"pricing":  prices,
		})
	})

	// Public groups (read-only, used on landing/pricing pages).
	v2.GET("/groups", func(c *gin.Context) {
		gs, err := store.ListGroups(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"groups": gs})
	})

	// Public health snapshot for /status (status.claude.com-style). Same
	// shape as /admin/health but stripped of error strings (operator-only)
	// and without the refresh trigger. Anonymous so the public status page
	// renders for visitors who aren't logged in — that page is the whole
	// point of having upstream credential health visible to users.
	v2.GET("/health", func(c *gin.Context) {
		hs, err := store.ListModelHealth(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Stable display-name counter per (provider × kind) — same scheme
		// as the admin endpoint so operators and users see identical labels.
		counters := map[string]int{}
		nameFor := func(authID, provider string) string {
			kind := "oauth"
			if len(authID) > 7 && authID[:7] == "apikey-" {
				kind = "api"
			} else if len(authID) > 7 && authID[:7] == "openai-" {
				kind = "api"
			} else if len(authID) > 10 && authID[:10] == "openai_api" {
				kind = "api"
			}
			prov := "claude"
			if provider == auth.ProviderOpenAI {
				prov = "codex"
			}
			key := prov + "-" + kind + "-" + authID
			if _, seen := counters[key]; !seen {
				grp := prov + "-" + kind
				counters[grp]++
				counters[key] = counters[grp]
			}
			return fmt.Sprintf("%s-%s-%03d", prov, kind, counters[key])
		}
		out := make([]gin.H, 0, len(hs))
		for _, rec := range hs {
			hist, _ := store.ListModelHealthHistory(c.Request.Context(), rec.AuthID, rec.Model, 90)
			histSlice := make([]gin.H, 0, len(hist))
			for _, r := range hist {
				histSlice = append(histSlice, gin.H{
					"status":     r.Status,
					"latency_ms": r.LatencyMs,
					"checked_at": r.CheckedAt.Unix(),
				})
			}
			out = append(out, gin.H{
				"id":           rec.ID,
				"display_name": nameFor(rec.AuthID, rec.Provider),
				"provider":     rec.Provider,
				"status":       rec.Status,
				"latency_ms":   rec.LatencyMs,
				"checked_at":   rec.CheckedAt.Unix(),
				"history":      histSlice,
				// `error` intentionally omitted — operator-only detail.
			})
		}
		c.JSON(http.StatusOK, gin.H{"checks": out, "as_of": time.Now().Unix()})
	})

	// Fleet-wide wallet aggregate is exposed to any signed-in user — it
	// powers the "Saved by us" tile on the operator console which itself
	// is open to all users (per the SSO design). No PII, just sums.
	authed.GET("/admin/wallet-totals", adminH.WalletTotalsHandler())

	// Admin (operator-only).
	adminG := authed.Group("/admin")
	adminG.Use(saasauth.RequireAdmin())
	adminH.Routes(adminG)
	if credH != nil {
		credH.Routes(adminG)
	}
	if legacyH != nil {
		legacyH.RegisterSaaSBridge(adminG)
	}
}
