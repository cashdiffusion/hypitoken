package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

type ctxKey string

const (
	CtxUser     ctxKey = "saas_user"
	CtxClaims   ctxKey = "saas_claims"
)

// RequireUser returns gin middleware that parses the Authorization: Bearer
// JWT and loads the user. 401s on missing/invalid token, 403s on disabled
// account.
func RequireUser(iss *Issuer, store *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("Authorization"))
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		if i := strings.IndexByte(raw, ' '); i > 0 {
			raw = strings.TrimSpace(raw[i+1:])
		}
		claims, err := iss.Parse(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		u, err := store.GetUser(c.Request.Context(), claims.UserID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
			return
		}
		if u.Disabled {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "account disabled"})
			return
		}
		c.Set(string(CtxUser), u)
		c.Set(string(CtxClaims), claims)
		c.Next()
	}
}

// RequireAdmin must come AFTER RequireUser. 403s non-admins.
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := c.Get(string(CtxUser))
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
			return
		}
		u, ok := v.(*db.User)
		if !ok || u.Role != db.RoleAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		c.Next()
	}
}

// CurrentUser pulls *db.User out of gin context. nil if not authed.
func CurrentUser(c *gin.Context) *db.User {
	v, ok := c.Get(string(CtxUser))
	if !ok {
		return nil
	}
	u, _ := v.(*db.User)
	return u
}
