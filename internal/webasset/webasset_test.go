package webasset

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func testServer(t *testing.T) (*gin.Engine, *Server) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := fstest.MapFS{
		"index.html":             {Data: []byte("<html>shell</html>")},
		"assets/index-abc123.js": {Data: []byte("console.log(1)")},
		"robots.txt":             {Data: []byte("User-agent: *")},
	}
	s := New(root)
	r := gin.New()
	r.GET("/*path", func(c *gin.Context) {
		name := c.Param("path")[1:]
		if name == "" {
			name = "index.html"
		}
		s.Serve(c, name)
	})
	return r, s
}

func get(t *testing.T, r *gin.Engine, path string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCacheControlByPath(t *testing.T) {
	r, _ := testServer(t)

	// Hashed build output is immutable — the URL changes when the bytes do.
	w := get(t, r, "/assets/index-abc123.js", nil)
	if got := w.Header().Get("Cache-Control"); got != immutableMaxAge {
		t.Errorf("assets Cache-Control = %q, want %q", got, immutableMaxAge)
	}
	if w.Header().Get("Content-Type") != "application/javascript; charset=utf-8" {
		t.Errorf("assets Content-Type = %q", w.Header().Get("Content-Type"))
	}

	// The shell's URL is stable across releases, so it must revalidate or a
	// deploy would never reach an already-open browser.
	w = get(t, r, "/index.html", nil)
	if got := w.Header().Get("Cache-Control"); got != revalidateAlways {
		t.Errorf("index Cache-Control = %q, want %q", got, revalidateAlways)
	}
}

func TestConditionalRequestReturns304(t *testing.T) {
	r, _ := testServer(t)

	w := get(t, r, "/index.html", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("first GET = %d", w.Code)
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the response")
	}

	w2 := get(t, r, "/index.html", map[string]string{"If-None-Match": etag})
	if w2.Code != http.StatusNotModified {
		t.Fatalf("revalidation = %d, want 304", w2.Code)
	}
	if w2.Body.Len() != 0 {
		t.Errorf("304 carried a %d-byte body", w2.Body.Len())
	}
}

func TestETagIsContentAddressed(t *testing.T) {
	r, _ := testServer(t)
	a := get(t, r, "/index.html", nil).Header().Get("ETag")
	b := get(t, r, "/assets/index-abc123.js", nil).Header().Get("ETag")
	if a == b {
		t.Errorf("distinct files share an ETag: %q", a)
	}
	// Same file twice must be stable, or every request would re-download.
	if again := get(t, r, "/index.html", nil).Header().Get("ETag"); again != a {
		t.Errorf("ETag not stable: %q then %q", a, again)
	}
}

func TestMissingAndTraversalReject(t *testing.T) {
	r, s := testServer(t)
	if w := get(t, r, "/nope.js", nil); w.Code != http.StatusNotFound {
		t.Errorf("missing file = %d, want 404", w.Code)
	}
	if s.Exists("../secret") {
		t.Error("Exists accepted a traversal path")
	}
	if s.Exists("assets") {
		t.Error("Exists accepted a directory")
	}
	if !s.Exists("robots.txt") {
		t.Error("Exists rejected a real file")
	}
}
