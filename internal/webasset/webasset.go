// Package webasset serves the embedded SPA build over HTTP with the caching
// semantics browsers expect from a Vite bundle.
//
// It exists because the naive version — `io.ReadAll` the embedded file and
// hand it to c.Data — was measurably expensive in production: the main chunk
// is 2.8 MB, so every single page load re-allocated it on the heap, and with
// no Cache-Control / ETag on the response the browser re-downloaded and
// re-parsed the whole bundle on every visit (measured: 1.77 s of the landing
// page's 2.28 s LCP was load delay). Files are read once, hashed once, and
// then served from memory with conditional-request support.
package webasset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// immutableMaxAge is the Cache-Control lifetime for content-hashed build
// output. Vite renames the file whenever its bytes change, so the URL can
// never go stale — `immutable` additionally tells the browser not to
// revalidate even on an explicit reload.
const immutableMaxAge = "public, max-age=31536000, immutable"

// revalidateAlways is for entry points whose URL is stable across releases
// (index.html, favicon.ico). `no-cache` still permits caching — it only
// forces a conditional request, which the ETag answers with a 304.
const revalidateAlways = "no-cache"

type entry struct {
	data []byte
	etag string
	mime string
}

// Server serves one embedded filesystem. Construct it once at mount time —
// the byte + digest cache is per-instance.
type Server struct {
	root fs.FS

	mu    sync.RWMutex
	cache map[string]*entry
}

func New(root fs.FS) *Server {
	return &Server{root: root, cache: make(map[string]*entry)}
}

// Exists reports whether name resolves to a regular file. Used by the SPA
// fallback to tell a root-level static file (robots.txt) apart from a
// client-side route that should receive the HTML shell.
func (s *Server) Exists(name string) bool {
	if !safeName(name) {
		return false
	}
	st, err := fs.Stat(s.root, name)
	return err == nil && !st.IsDir()
}

// Serve writes name to the response with an ETag and a Cache-Control chosen
// from the path: content-hashed build output under assets/ is immutable for a
// year, everything else revalidates. Conditional requests are answered by
// http.ServeContent, so a repeat visitor gets a 304 with an empty body.
func (s *Server) Serve(c *gin.Context, name string) {
	e, err := s.load(name)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	h := c.Writer.Header()
	h.Set("Content-Type", e.mime)
	h.Set("ETag", e.etag)
	if strings.HasPrefix(name, "assets/") {
		h.Set("Cache-Control", immutableMaxAge)
	} else {
		h.Set("Cache-Control", revalidateAlways)
	}
	// A zero modTime makes ServeContent skip Last-Modified and rely on the
	// ETag alone. That is deliberate: embed.FS reports no useful mtime, and a
	// build-time constant would only churn the cache on every restart.
	http.ServeContent(c.Writer, c.Request, name, time.Time{}, bytes.NewReader(e.data))
}

func (s *Server) load(name string) (*entry, error) {
	if !safeName(name) {
		return nil, fs.ErrNotExist
	}
	s.mu.RLock()
	e, ok := s.cache[name]
	s.mu.RUnlock()
	if ok {
		return e, nil
	}

	f, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	e = &entry{
		data: data,
		etag: `"` + hex.EncodeToString(sum[:8]) + `"`,
		mime: mimeFor(name),
	}

	s.mu.Lock()
	// Another goroutine may have raced us here; either copy is equivalent, so
	// keep whichever landed first to avoid handing out two backing arrays.
	if existing, ok := s.cache[name]; ok {
		e = existing
	} else {
		s.cache[name] = e
	}
	s.mu.Unlock()
	return e, nil
}

// safeName rejects anything that could escape the embedded root. The dist FS
// is trusted, but the names reach us from request paths.
func safeName(name string) bool {
	if name == "" || strings.Contains(name, "..") {
		return false
	}
	return path.Clean(name) == name && !strings.HasPrefix(name, "/")
}

func mimeFor(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json", ".map":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
