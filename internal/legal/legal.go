// Package legal hosts temporary screenshot-target pages for the Alipay /
// Stripe auto-debit Due Diligence Questionnaire (DDQ). The pages mock the
// subscription flow that will go live once the payment provider approves the
// merchant application — they live at /legal/* and are intentionally not
// linked from any real user-facing surface.
package legal

import (
	"embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed pages/*.html
var pagesFS embed.FS

// Register mounts the screenshot-target pages on the given engine. Pages are
// reachable at /legal/pricing, /legal/subscribe, /legal/support, and
// /legal/refund-policy. The /legal/ root redirects to the pricing page.
func Register(r *gin.Engine) {
	r.GET("/legal/", redirectToPricing)
	r.GET("/legal", redirectToPricing)
	r.GET("/legal/pricing", servePage("pages/pricing.html"))
	r.GET("/legal/subscribe", servePage("pages/subscribe.html"))
	r.GET("/legal/support", servePage("pages/support.html"))
	r.GET("/legal/refund-policy", servePage("pages/refund-policy.html"))
}

func redirectToPricing(c *gin.Context) {
	c.Redirect(http.StatusFound, "/legal/pricing")
}

func servePage(path string) gin.HandlerFunc {
	data, err := pagesFS.ReadFile(path)
	return func(c *gin.Context) {
		if err != nil {
			c.String(http.StatusInternalServerError, "page unavailable: %v", err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	}
}
