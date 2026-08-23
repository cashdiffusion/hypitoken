package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

const bridgeSecret = "test-jwt-secret-shared-with-hypihub"

func bridgeStore(t *testing.T) *db.DB {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "sso.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// bridgeCall runs the hook against a request carrying tok as its bearer token.
func bridgeCall(t *testing.T, hook func(*gin.Context) (bool, bool, error), tok string) (bool, bool, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/auths/1", nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return hook(c)
}

// TestLegacyAdminSSORejectsForgedAdminClaim is the core assertion of the
// privilege-escalation fix. saas.jwt_secret is shared with the sibling HypiHub
// gateway, so "holds the secret" has to stay strictly weaker than "is an
// operator here". Both forgeries below are perfectly signed — the old hook,
// which returned claims.Role == "admin" without reading the database, accepted
// both and handed them the legacy console's mutation endpoints (delete an
// upstream credential, patch/reset a client token).
func TestLegacyAdminSSORejectsForgedAdminClaim(t *testing.T) {
	store := bridgeStore(t)
	iss := NewIssuer(bridgeSecret, time.Hour)
	hook := LegacyAdminSSO(iss, store)

	ordinary, err := store.CreateUser(context.Background(), "user@example.com", "x", db.RoleUser, 1, true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Run("uid that has no row at all", func(t *testing.T) {
		// Issue() takes the role as a parameter, so this *is* the attack: a
		// token signed with the real secret claiming a user id that was never
		// created. Nothing to authorize against — it must not even be allowed.
		tok, _, err := iss.Issue(999999, db.RoleAdmin)
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		allowed, isAdmin, err := bridgeCall(t, hook, tok)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if allowed || isAdmin {
			t.Fatalf("forged token for a nonexistent uid was accepted (allowed=%v isAdmin=%v); it would reach every /admin/api/* mutation", allowed, isAdmin)
		}
	})

	t.Run("real user whose token claims admin", func(t *testing.T) {
		tok, _, err := iss.Issue(ordinary.ID, db.RoleAdmin)
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		allowed, isAdmin, err := bridgeCall(t, hook, tok)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !allowed {
			t.Fatal("a live non-admin user must still be allowed (read-only console access is the point of the bridge)")
		}
		if isAdmin {
			t.Fatal("role came from the token, not the database: a self-signed role=admin claim escalated an ordinary account")
		}
	})

	t.Run("demoted admin", func(t *testing.T) {
		// Token minted while the user really was an admin, then the role is
		// revoked. The token is still valid and still says admin.
		admin, err := store.CreateUser(context.Background(), "demoted@example.com", "x", db.RoleAdmin, 1, true)
		if err != nil {
			t.Fatalf("create admin: %v", err)
		}
		tok, _, err := iss.Issue(admin.ID, db.RoleAdmin)
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		if _, _, err := bridgeCall(t, hook, tok); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := store.SetUserRole(context.Background(), admin.ID, db.RoleUser); err != nil {
			t.Fatalf("demote: %v", err)
		}
		// No cache sits in front of the lookup, so the demotion is effective
		// on the very next request rather than after some TTL.
		allowed, isAdmin, err := bridgeCall(t, hook, tok)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !allowed {
			t.Fatal("a demoted admin is still a signed-in user and keeps read access")
		}
		if isAdmin {
			t.Fatal("demotion did not take effect immediately: the outstanding token still granted operator rights")
		}
	})
}

// TestLegacyAdminSSOHonoursTheStore covers the paths that must keep working
// (a real admin) and the ones the store alone can decide (disabled account).
func TestLegacyAdminSSOHonoursTheStore(t *testing.T) {
	store := bridgeStore(t)
	iss := NewIssuer(bridgeSecret, time.Hour)
	hook := LegacyAdminSSO(iss, store)
	ctx := context.Background()

	admin, err := store.CreateUser(ctx, "root@example.com", "x", db.RoleAdmin, 1, true)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	// A genuine admin logging in normally: Issue() is handed the stored role.
	tok, _, err := iss.Issue(admin.ID, admin.Role)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	allowed, isAdmin, err := bridgeCall(t, hook, tok)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed || !isAdmin {
		t.Fatalf("a real admin lost console access (allowed=%v isAdmin=%v)", allowed, isAdmin)
	}

	// Even a token that honestly says "admin" stops working the moment the
	// account is disabled.
	if err := store.SetUserDisabled(ctx, admin.ID, true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	allowed, isAdmin, err = bridgeCall(t, hook, tok)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed || isAdmin {
		t.Fatalf("disabled account still authorized (allowed=%v isAdmin=%v)", allowed, isAdmin)
	}
}

// TestLegacyAdminSSOFailsClosedOnStoreError locks the "don't fall back to the
// claims" half of the fix: when the store cannot answer, the hook reports an
// error so the gate can 503. Returning (false, false, nil) would be almost as
// bad in the other direction — it is indistinguishable from "bad token" and
// would hide an outage behind a 401.
func TestLegacyAdminSSOFailsClosedOnStoreError(t *testing.T) {
	store := bridgeStore(t)
	iss := NewIssuer(bridgeSecret, time.Hour)
	hook := LegacyAdminSSO(iss, store)

	u, err := store.CreateUser(context.Background(), "admin@example.com", "x", db.RoleAdmin, 1, true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	tok, _, err := iss.Issue(u.ID, u.Role)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	allowed, isAdmin, err := bridgeCall(t, hook, tok)
	if err == nil {
		t.Fatal("store failure was not reported; the gate cannot tell an outage from a rejection")
	}
	if allowed || isAdmin {
		t.Fatalf("failed open on a store error (allowed=%v isAdmin=%v)", allowed, isAdmin)
	}
}

func TestLegacyAdminSSOMalformedCredentials(t *testing.T) {
	store := bridgeStore(t)
	hook := LegacyAdminSSO(NewIssuer(bridgeSecret, time.Hour), store)

	cases := map[string]string{
		"no token":       "",
		"garbage":        "not-a-jwt",
		"other secret":   mustIssue(t, NewIssuer("a-different-secret", time.Hour), 1, db.RoleAdmin),
		"expired admin":  signClaims(t, bridgeSecret, Claims{UserID: 1, Role: db.RoleAdmin, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour))}}),
		"unsigned claim": "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJ1aWQiOjEsInJvbGUiOiJhZG1pbiJ9.",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			allowed, isAdmin, err := bridgeCall(t, hook, tok)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if allowed || isAdmin {
				t.Fatalf("accepted %s (allowed=%v isAdmin=%v)", name, allowed, isAdmin)
			}
		})
	}
}

func mustIssue(t *testing.T, iss *Issuer, uid int64, role string) string {
	t.Helper()
	tok, _, err := iss.Issue(uid, role)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return tok
}

// signClaims mints a token with hand-written claims, for shapes Issue cannot
// produce (an already-expired token, or a pre-audience one).
func signClaims(t *testing.T, secret string, c Claims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

// TestIssueStampsAudience documents the forward-compatibility half of the
// change: new tokens carry `aud`, and Parse still accepts a token without one
// so sessions minted before this deploy survive until they expire naturally.
func TestIssueStampsAudience(t *testing.T) {
	iss := NewIssuer(bridgeSecret, time.Hour)
	tok, _, err := iss.Issue(7, db.RoleUser)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := iss.Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != AudienceSession {
		t.Fatalf("aud = %v, want [%s]", claims.Audience, AudienceSession)
	}

	// A session minted before `aud` existed. Parse must keep accepting it, or
	// deploying this change signs out everyone currently logged in.
	legacy := signClaims(t, bridgeSecret, Claims{
		UserID:           7,
		Role:             db.RoleUser,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	})
	got, err := iss.Parse(legacy)
	if err != nil {
		t.Fatalf("pre-audience token rejected: %v", err)
	}
	if len(got.Audience) != 0 {
		t.Fatalf("aud = %v, want empty", got.Audience)
	}
}
