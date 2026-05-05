package adapter

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// MountSPA serves the embedded SaaS SPA at the root path. The Go process
// keeps full control over /v1/*, /api/v2/*, /healthz, /mgmt-console/*, etc.;
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

	// Specific route for /assets/* (hashed Vite chunks). gin's radix tree is
	// stricter than net/http: register this BEFORE NoRoute kicks in.
	engine.GET("/assets/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		if p == "" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		serveAsset(c, dist, "assets/"+p)
	})

	engine.GET("/", func(c *gin.Context) { serveAsset(c, dist, "index.html") })

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
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/v1/") {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		// Try a literal asset (favicon.ico, robots.txt, …) first.
		clean := strings.TrimPrefix(path, "/")
		if clean != "" && !strings.Contains(clean, "..") {
			if _, err := fs.Stat(dist, clean); err == nil {
				serveAsset(c, dist, clean)
				return
			}
		}
		serveAsset(c, dist, "index.html")
	})
}

func serveAsset(c *gin.Context, root fs.FS, name string) {
	f, err := root.Open(name)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, mimeFor(name), data)
}

func mimeFor(name string) string {
	switch filepath.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}

// SPAFromEmbed is a tiny helper for callers who have an embed.FS rooted at
// some path containing the dist directory.
func SPAFromEmbed(efs embed.FS, prefix string) (fs.FS, error) {
	return fs.Sub(efs, prefix)
}
