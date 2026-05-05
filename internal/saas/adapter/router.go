package adapter

import (
	"net/http"

	"github.com/gin-gonic/gin"

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
