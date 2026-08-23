package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

// ssoGateEngine mounts adminAuth in front of a probe route, with SSOAuth
// stubbed to the given decision. The operator token is set to something the
// test never sends, so every request lands on the SSO path.
func ssoGateEngine(t *testing.T, hook func(*gin.Context) (bool, bool, error)) (*gin.Engine, *bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	prev := SSOAuth
	SSOAuth = hook
	t.Cleanup(func() { SSOAuth = prev })

	h := &Handler{cfg: &config.Config{AdminToken: "the-operator-password"}}
	eng := gin.New()
	g := eng.Group("/admin/api", h.adminAuth())
	privileged := new(bool)
	handler := func(c *gin.Context) {
		*privileged = c.GetBool(ctxKeyPrivileged)
		c.Status(http.StatusOK)
	}
	g.GET("/__probe", handler)
	g.DELETE("/__probe", handler)
	return eng, privileged
}

func ssoGateCall(eng *gin.Engine, method string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/admin/api/__probe", nil)
	req.Header.Set("Authorization", "Bearer a-saas-session-jwt")
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)
	return w
}

// TestAdminAuthSSOForgedAdminCannotMutate is the gate-level half of the
// privilege-escalation fix. The bridge decides the role from saas.db (see
// saas/auth.LegacyAdminSSO); a forged token claiming role=admin therefore
// arrives here as isAdmin=false, and the gate must refuse every write. Before
// the fix the same forged token arrived as isAdmin=true and sailed through to
// handlers that delete upstream credentials and reset client tokens.
func TestAdminAuthSSOForgedAdminCannotMutate(t *testing.T) {
	// What the bridge reports for a signed-but-forged role=admin token held by
	// an ordinary (or nonexistent, hence not-allowed) account.
	eng, privileged := ssoGateEngine(t, func(*gin.Context) (bool, bool, error) {
		return true, false, nil
	})

	if w := ssoGateCall(eng, http.MethodDelete); w.Code != http.StatusForbidden {
		t.Fatalf("DELETE with a forged admin claim: got %d, want 403 — the mutation reached the handler", w.Code)
	}

	// The read path stays open (that is the point of the bridge), but the
	// request must not be marked privileged or read-only handlers stop
	// redacting fleet-internal detail for it.
	w := ssoGateCall(eng, http.MethodGet)
	if w.Code != http.StatusOK {
		t.Fatalf("GET for a signed-in non-admin: got %d, want 200", w.Code)
	}
	if *privileged {
		t.Fatal("non-admin SSO caller was flagged privileged; read-only handlers would hand it fleet-internal detail")
	}
}

func TestAdminAuthSSODecisions(t *testing.T) {
	cases := []struct {
		name       string
		hook       func(*gin.Context) (bool, bool, error)
		method     string
		wantCode   int
		wantPrivil bool
	}{
		{
			// The experience a real admin must keep: writes go through.
			name:       "stored admin may mutate",
			hook:       func(*gin.Context) (bool, bool, error) { return true, true, nil },
			method:     http.MethodDelete,
			wantCode:   http.StatusOK,
			wantPrivil: true,
		},
		{
			name:     "unknown or disabled account",
			hook:     func(*gin.Context) (bool, bool, error) { return false, false, nil },
			method:   http.MethodGet,
			wantCode: http.StatusUnauthorized,
		},
		{
			// The store could not answer. Fail closed rather than fall back to
			// whatever the token claims — a database hiccup must not become an
			// authorization upgrade, and must not be silently downgraded to a
			// 401 either.
			name:     "store unavailable, write",
			hook:     func(*gin.Context) (bool, bool, error) { return false, false, errors.New("database is closed") },
			method:   http.MethodDelete,
			wantCode: http.StatusServiceUnavailable,
		},
		{
			name:     "store unavailable, read",
			hook:     func(*gin.Context) (bool, bool, error) { return false, false, errors.New("database is closed") },
			method:   http.MethodGet,
			wantCode: http.StatusServiceUnavailable,
		},
		{
			// A hook that both errors and claims admin must still be refused:
			// the error is the authoritative half.
			name:     "store unavailable outranks an optimistic decision",
			hook:     func(*gin.Context) (bool, bool, error) { return true, true, errors.New("database is closed") },
			method:   http.MethodDelete,
			wantCode: http.StatusServiceUnavailable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng, privileged := ssoGateEngine(t, tc.hook)
			w := ssoGateCall(eng, tc.method)
			if w.Code != tc.wantCode {
				t.Fatalf("got %d, want %d (body %s)", w.Code, tc.wantCode, w.Body.String())
			}
			if tc.wantCode == http.StatusOK && *privileged != tc.wantPrivil {
				t.Fatalf("privileged = %v, want %v", *privileged, tc.wantPrivil)
			}
		})
	}
}

// TestAdminAuthLegacyTokenUnaffected: the operator password never consults the
// SSO hook, so a store outage cannot lock operators out of the console.
func TestAdminAuthLegacyTokenUnaffected(t *testing.T) {
	eng, privileged := ssoGateEngine(t, func(*gin.Context) (bool, bool, error) {
		t.Fatal("SSO hook consulted for a legacy operator token")
		return false, false, nil
	})

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/__probe", nil)
	req.Header.Set("X-Admin-Token", "the-operator-password")
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("legacy operator token: got %d, want 200", w.Code)
	}
	if !*privileged {
		t.Fatal("legacy operator token was not flagged privileged")
	}
}
