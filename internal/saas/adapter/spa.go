package adapter

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/webasset"
)

// MountSPA serves the embedded SaaS SPA at the root path. The Go process
// keeps full control over /v1/*, /api/v2/*, /admin/api/*, /healthz, etc.;
// every other path falls through to the React app's HTML shell so that
// client-side routing works on hard-refresh.
//
// The dist FS is provided by the caller (typically passed in from the admin
// package's embedded web/dist). When dist is nil or empty, MountSPA returns
// without registering any routes — useful for backend-only test builds.
func MountSPA(engine *gin.Engine, dist fs.FS) {
	if dist == nil {
		return
	}
	// Confirm there's actually an index.html — empty embed = nothing to serve.
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return
	}

	assets := webasset.New(dist)

	// Specific route for /assets/* (hashed Vite chunks). gin's radix tree is
	// stricter than net/http: register this BEFORE NoRoute kicks in.
	engine.GET("/assets/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		if p == "" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		assets.Serve(c, "assets/"+p)
	})

	engine.GET("/", func(c *gin.Context) { assets.Serve(c, "index.html") })

	// SPA fallback. Reaching NoRoute means no API or admin route matched, so
	// it's safe to assume the request is a browser navigation that should be
	// resolved client-side by react-router. Static assets at the root level
	// (favicon, robots, etc.) get a one-shot lookup before falling back.
	engine.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if c.Request.Method != http.MethodGet {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		// Don't intercept anything that looks like an API namespace —
		// /api/v2 already has its own routes; reaching this branch with that
		// prefix means a typo, which deserves a 404.
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/admin/") {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		// Try a literal asset (favicon.ico, robots.txt, …) first.
		clean := strings.TrimPrefix(path, "/")
		if assets.Exists(clean) {
			assets.Serve(c, clean)
			return
		}
		assets.Serve(c, "index.html")
	})
}

// SPAFromEmbed is a tiny helper for callers who have an embed.FS rooted at
// some path containing the dist directory.
func SPAFromEmbed(efs embed.FS, prefix string) (fs.FS, error) {
	return fs.Sub(efs, prefix)
}
