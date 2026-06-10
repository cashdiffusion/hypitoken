package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRegisterSaaSBridgeMarksPrivileged locks the fix for the empty Requests
// tab: the SaaS bridge group is mounted behind RequireAdmin (operator-only),
// so every request reaching it must be flagged privileged. Without the flag,
// handleRequestsQuery falls into its redaction branch and nulls res.Entries —
// the symptom the user saw at /app/admin/requests (summary present, zero rows).
func TestRegisterSaaSBridgeMarksPrivileged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	eng := gin.New()
	g := eng.Group("/api/v2/admin")
	h.RegisterSaaSBridge(g)

	// Probe route inherits the group's middleware (g.Use persists for routes
	// registered after it), so it observes exactly what the bridge handlers do.
	var got bool
	g.GET("/__probe", func(c *gin.Context) {
		got = c.GetBool(ctxKeyPrivileged)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/__probe", nil)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)

	if !got {
		t.Fatal("bridge group did not set ctxKeyPrivileged; handleRequestsQuery would null out entries and the Requests tab renders empty")
	}
}
