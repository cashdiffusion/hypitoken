package adapter

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/auth"
	legacyadmin "github.com/wjsoj/CPA-Claude/internal/admin"
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
func Mount(engine *gin.Engine, store *db.DB, authH *saasauth.Handler, tokensH *tokens.Handler, billingH *billing.Handler, adminH *admin.Handler, credH *admin.CredHandler, iss *saasauth.Issuer, legacyH *legacyadmin.Handler) {
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
