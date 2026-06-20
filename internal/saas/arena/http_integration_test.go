package arena

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// buildTestEngine wires a real DB + issuer + arena service onto a gin engine,
// mirroring how adapter.Mount registers the routes (authed leaderboard +
// public SSE stream).
func buildTestEngine(t *testing.T) (*gin.Engine, *db.DB, *saasauth.Issuer, *Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, err := db.Open(filepath.Join(t.TempDir(), "arena.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	iss := saasauth.NewIssuer("test-secret", time.Hour)
	svc := New(store, iss)

	r := gin.New()
	v2 := r.Group("/api/v2")
	svc.PublicRoutes(v2)
	authed := v2.Group("")
	authed.Use(saasauth.RequireUser(iss, store))
	svc.AuthedRoutes(authed)
	return r, store, iss, svc
}

func mkUser(t *testing.T, store *db.DB, email string) int64 {
	t.Helper()
	u, err := store.CreateUser(context.Background(), email, "h", "user", 1, true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}

func tokenFor(t *testing.T, iss *saasauth.Issuer, uid int64) string {
	t.Helper()
	s, _, err := iss.Issue(uid, "user")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return s
}

func TestLeaderboardEndpoint(t *testing.T) {
	r, store, iss, _ := buildTestEngine(t)
	ctx := context.Background()
	u1 := mkUser(t, store, "u1@x.com")
	u2 := mkUser(t, store, "u2@x.com")
	// u1 opts in (real name); u2 stays anonymous.
	name := "Topcoder"
	yes := true
	if _, err := store.UpdateProfile(ctx, u1, &name, &yes); err != nil {
		t.Fatal(err)
	}
	store.BumpActivity(ctx, u1, 500)
	store.BumpActivity(ctx, u2, 100)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/arena/leaderboard?metric=tokens", nil)
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, iss, u2))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Metric string      `json:"metric"`
		Rows   []leaderRow `json:"rows"`
		You    *leaderRow  `json:"you"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(resp.Rows))
	}
	// u1 leads and shows its real opted-in name.
	if resp.Rows[0].Name != "Topcoder" || !resp.Rows[0].Public {
		t.Fatalf("row0 identity wrong: %+v", resp.Rows[0])
	}
	// u2 is anonymous — must NOT be its email, must be a pseudonym, flagged you.
	if resp.Rows[1].Public {
		t.Fatal("u2 should be anonymous")
	}
	if !resp.Rows[1].IsYou {
		t.Fatal("u2 row should be flagged is_you for the u2 viewer")
	}
	if strings.Contains(resp.Rows[1].Name, "@") {
		t.Fatalf("anonymous row leaked an email: %q", resp.Rows[1].Name)
	}
	if resp.You == nil || resp.You.Rank != 2 {
		t.Fatalf("you rank wrong: %+v", resp.You)
	}
}

func TestLeaderboardRequiresAuth(t *testing.T) {
	r, _, _, _ := buildTestEngine(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/arena/leaderboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestStreamAuthViaQueryParam(t *testing.T) {
	r, store, iss, svc := buildTestEngine(t)
	uid := mkUser(t, store, "stream@x.com")
	tok := tokenFor(t, iss, uid)

	srv := httptest.NewServer(r)
	defer srv.Close()

	// Connect to the SSE stream using the query-param token (EventSource style).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v2/arena/stream?access_token="+tok, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type %q", ct)
	}

	// Publish a pulse once the subscriber has registered (from a goroutine, so
	// the main goroutine can block on the blocking SSE read).
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for svc.Hub.SubscriberCount() == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		svc.OnCharge(uid, "anthropic", "claude-opus-4-8", 1234)
	}()

	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data: {") {
				got <- line
				return
			}
		}
	}()

	select {
	case line := <-got:
		if !strings.Contains(line, "\"tokens\":1234") {
			t.Fatalf("event missing token count: %s", line)
		}
		if !strings.Contains(line, "\"is_you\":true") {
			t.Fatalf("event should be flagged is_you for the same user: %s", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("did not receive SSE event within 3s")
	}
}

func TestStreamRejectsBadToken(t *testing.T) {
	r, _, _, _ := buildTestEngine(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/arena/stream?access_token=garbage", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for bad token, got %d", w.Code)
	}
}
