package usage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// The team reports must be reachable ONLY by an admin of that exact workspace.
// This exercises the real RequireWorkspaceAdmin middleware rather than trusting
// that the routes were mounted behind it.
func TestTeamRoutesRequireWorkspaceAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	d := testDB(t)

	wsA, err := d.CreateEnterpriseWorkspace(ctx, "acme", 100, 0, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("ws a: %v", err)
	}
	wsB, err := d.CreateEnterpriseWorkspace(ctx, "globex", 100, 0, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("ws b: %v", err)
	}

	boss, err := d.CreateUser(ctx, "boss@acme.com", "h", "user", 1, true)
	if err != nil {
		t.Fatalf("boss: %v", err)
	}
	grunt, err := d.CreateUser(ctx, "grunt@acme.com", "h", "user", 1, true)
	if err != nil {
		t.Fatalf("grunt: %v", err)
	}
	if err := d.UpsertWorkspaceMember(ctx, wsA.ID, boss.ID, db.WSRoleAdmin); err != nil {
		t.Fatalf("boss membership: %v", err)
	}
	if err := d.UpsertWorkspaceMember(ctx, wsA.ID, grunt.ID, db.WSRoleMember); err != nil {
		t.Fatalf("grunt membership: %v", err)
	}

	// Stand up the real chain: a stub that injects the caller, then the real
	// RequireWorkspaceAdmin, then the usage routes.
	newRouter := func(as *db.User) *gin.Engine {
		r := gin.New()
		g := r.Group("/workspaces/:id")
		g.Use(func(c *gin.Context) {
			c.Set(string(saasauth.CtxUser), as)
			c.Next()
		})
		g.Use(saasauth.RequireWorkspaceAdmin(d))
		New(d).TeamRoutes(g.Group("/usage"))
		return r
	}

	get := func(as *db.User, wsID int64, path string) int {
		w := httptest.NewRecorder()
		newRouter(as).ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			"/workspaces/"+itoa(wsID)+"/usage"+path, nil))
		return w.Code
	}

	for _, path := range []string{"/summary", "/tokens", "/rows", "/export.csv"} {
		// The space's own admin gets in.
		if code := get(boss, wsA.ID, path); code != http.StatusOK {
			t.Errorf("admin of own workspace: %s → %d, want 200", path, code)
		}
		// A plain member of the same space does NOT — they can see their own
		// spend via the personal view, never their colleagues'.
		if code := get(grunt, wsA.ID, path); code != http.StatusForbidden {
			t.Errorf("member of own workspace: %s → %d, want 403", path, code)
		}
		// An admin of a DIFFERENT space is a stranger here.
		if code := get(boss, wsB.ID, path); code != http.StatusForbidden {
			t.Errorf("admin of another workspace: %s → %d, want 403", path, code)
		}
	}
}

// A personal caller asking about a key that isn't theirs gets a 404, not another
// user's numbers.
func TestPersonalScopeRejectsForeignToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	d := testDB(t)

	mine, err := d.CreateUser(ctx, "mine@example.com", "h", "user", 1, true)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	theirs, err := d.CreateUser(ctx, "theirs@example.com", "h", "user", 1, true)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	foreign, err := d.CreateUserToken(ctx, theirs.ID, db.TokenParams{Name: "not-yours"})
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	r := gin.New()
	g := r.Group("/me/usage")
	g.Use(func(c *gin.Context) {
		c.Set(string(saasauth.CtxUser), mine)
		c.Next()
	})
	New(d).PersonalRoutes(g)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/me/usage/summary?token_id="+itoa(foreign.ID), nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("querying someone else's key → %d, want 404", w.Code)
	}
}
