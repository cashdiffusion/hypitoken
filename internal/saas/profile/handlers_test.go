package profile

import (
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

func buildEngine(t *testing.T) (*gin.Engine, *db.DB, *saasauth.Issuer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, err := db.Open(filepath.Join(t.TempDir(), "profile.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	iss := saasauth.NewIssuer("secret", time.Hour)
	h := New(store)
	r := gin.New()
	authed := r.Group("/api/v2")
	authed.Use(saasauth.RequireUser(iss, store))
	h.Routes(authed)
	return r, store, iss
}

func auth(t *testing.T, iss *saasauth.Issuer, uid int64) string {
	t.Helper()
	s, _, err := iss.Issue(uid, "user")
	if err != nil {
		t.Fatal(err)
	}
	return "Bearer " + s
}

func TestProfileGetCreatesDefault(t *testing.T) {
	r, store, iss := buildEngine(t)
	u, _ := store.CreateUser(context.Background(), "p@x.com", "h", "user", 1, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/me/profile", nil)
	req.Header.Set("Authorization", auth(t, iss, u.ID))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var v profileView
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if !v.NameIsDefault || v.PublicOptIn || !strings.HasPrefix(v.DisplayName, "hypi-") {
		t.Fatalf("unexpected default profile: %+v", v)
	}
}

func TestProfilePatchValidation(t *testing.T) {
	r, store, iss := buildEngine(t)
	u, _ := store.CreateUser(context.Background(), "p2@x.com", "h", "user", 1, true)

	// Too-short nickname → 400.
	bad := httptest.NewRequest(http.MethodPatch, "/api/v2/me/profile", strings.NewReader(`{"display_name":"a"}`))
	bad.Header.Set("Authorization", auth(t, iss, u.ID))
	bad.Header.Set("Content-Type", "application/json")
	wb := httptest.NewRecorder()
	r.ServeHTTP(wb, bad)
	if wb.Code != http.StatusBadRequest {
		t.Fatalf("short nick want 400, got %d", wb.Code)
	}

	// Valid rename + opt-in.
	ok := httptest.NewRequest(http.MethodPatch, "/api/v2/me/profile",
		strings.NewReader(`{"display_name":"  Rocket Dev  ","public_opt_in":true}`))
	ok.Header.Set("Authorization", auth(t, iss, u.ID))
	ok.Header.Set("Content-Type", "application/json")
	wo := httptest.NewRecorder()
	r.ServeHTTP(wo, ok)
	if wo.Code != http.StatusOK {
		t.Fatalf("valid patch status %d: %s", wo.Code, wo.Body.String())
	}
	var v profileView
	_ = json.Unmarshal(wo.Body.Bytes(), &v)
	if v.DisplayName != "Rocket Dev" || v.NameIsDefault || !v.PublicOptIn {
		t.Fatalf("patch not applied (trim/flags): %+v", v)
	}
}

func TestGreetingFromHeaders(t *testing.T) {
	r, store, iss := buildEngine(t)
	u, _ := store.CreateUser(context.Background(), "g@x.com", "h", "user", 1, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/me/greeting", nil)
	req.Header.Set("Authorization", auth(t, iss, u.ID))
	req.Header.Set("CF-IPCountry", "jp")
	req.Header.Set("CF-IPCity", "Osaka")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var v greetView
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if v.CountryCode != "JP" || v.City != "Osaka" {
		t.Fatalf("greeting wrong: %+v", v)
	}
}

func TestGreetingPlaceholderCountryDropped(t *testing.T) {
	r, store, iss := buildEngine(t)
	u, _ := store.CreateUser(context.Background(), "g2@x.com", "h", "user", 1, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/me/greeting", nil)
	req.Header.Set("Authorization", auth(t, iss, u.ID))
	req.Header.Set("CF-IPCountry", "XX") // CF "unknown" placeholder
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var v greetView
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if v.CountryCode != "" {
		t.Fatalf("placeholder country should be dropped, got %q", v.CountryCode)
	}
}
