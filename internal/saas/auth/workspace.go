package auth

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// RequireWorkspaceAdmin gates a /workspaces/:id/* route to the workspace's own
// admins (a per-resource check, distinct from the platform-wide RequireAdmin).
// A platform admin (role=admin) is allowed through to any workspace. Must come
// AFTER RequireUser. On success the resolved workspace id is stored in context.
func RequireWorkspaceAdmin(store *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		u := CurrentUser(c)
		if u == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
			return
		}
		wsID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || wsID <= 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "bad workspace id"})
			return
		}
		// Platform admins bypass the membership check.
		if u.Role == db.RoleAdmin {
			c.Set("workspace_id", wsID)
			c.Next()
			return
		}
		m, err := store.GetWorkspaceMember(c.Request.Context(), wsID, u.ID)
		if err != nil || m.Role != db.WSRoleAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "workspace admin only"})
			return
		}
		c.Set("workspace_id", wsID)
		c.Next()
	}
}
