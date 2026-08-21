package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas"
	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

const (
	ssoHub        = "https://hub.novadiffusion.com"
	ssoReturnURL  = ssoHub + "/sso"
	ssoTestSecret = "sso-test-secret"
)

type ssoFixture struct {
	engine *gin.Engine
	store  *db.DB
	iss    *saasauth.Issuer
	userID int64
	jwt    string
}

// newSSOFixture stands up both halves of the handoff on one engine: the authed
// minting route and the service-token-gated redeem route.
func newSSOFixture(t *testing.T, origins []string) *ssoFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, err := db.Open(filepath.Join(t.TempDir(), "sso.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	u, err := store.CreateUser(ctx, "sso@example.com", "hash", db.RoleUser, 1, true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	iss := saasauth.NewIssuer(ssoTestSecret, time.Hour)
	tok, _, err := iss.Issue(u.ID, u.Role)
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}

	allow, err := saas.NewOriginAllowlist(origins)
	if err != nil {
		t.Fatalf("allowlist: %v", err)
	}

	engine := gin.New()
	v2 := engine.Group("/api/v2")

	set, err := NewServiceTokenSet([]string{testSvcToken})
	if err != nil {
		t.Fatalf("token set: %v", err)
	}
	NewServiceHandler(NewAdapter(store, nil, nil), set).WithIssuer(iss).Mount(v2)

	authed := v2.Group("")
	authed.Use(saasauth.RequireUser(iss, store))
	NewSSOHandler(store, allow).AuthedRoutes(authed)

	return &ssoFixture{engine: engine, store: store, iss: iss, userID: u.ID, jwt: tok}
}

func (f *ssoFixture) post(t *testing.T, path string, body any, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.engine.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (f *ssoFixture) mint(t *testing.T, returnURL string) (int, map[string]any) {
	t.Helper()
	return f.post(t, "/api/v2/auth/sso/code", gin.H{"return_url": returnURL},
		map[string]string{"Authorization": "Bearer " + f.jwt})
}

func (f *ssoFixture) redeem(t *testing.T, code string) (int, map[string]any) {
	t.Helper()
	return f.post(t, "/api/v2/svc/sso/redeem", gin.H{"code": code},
		map[string]string{"X-Service-Token": testSvcToken})
}

// TestSSOHandoffRoundTrip walks the frozen flow: mint here, redeem there, and
// the JWT that comes back must be a normal session for the same user.
func TestSSOHandoffRoundTrip(t *testing.T) {
	f := newSSOFixture(t, []string{ssoHub})

	status, body := f.mint(t, ssoReturnURL)
	if status != http.StatusOK {
		t.Fatalf("mint status = %d, want 200 (body %v)", status, body)
	}
	code, _ := body["code"].(string)
	if len(code) != 43 {
		t.Fatalf("code = %q (len %d), want 43 chars", code, len(code))
	}
	if got := body["expires_in"]; got != float64(120) {
		t.Fatalf("expires_in = %v, want 120", got)
	}
	if got := body["return_url"]; got != ssoReturnURL {
		t.Fatalf("return_url = %v, want %q", got, ssoReturnURL)
	}

	status, body = f.redeem(t, code)
	if status != http.StatusOK {
		t.Fatalf("redeem status = %d, want 200 (body %v)", status, body)
	}
	tok, _ := body["token"].(string)
	if tok == "" {
		t.Fatal("redeem returned no token")
	}
	claims, err := f.iss.Parse(tok)
	if err != nil {
		t.Fatalf("issued token does not verify with the normal issuer: %v", err)
	}
	if claims.UserID != f.userID {
		t.Fatalf("token uid = %d, want %d", claims.UserID, f.userID)
	}
	user, _ := body["user"].(map[string]any)
	if user == nil {
		t.Fatal("redeem returned no user block")
	}
	for _, k := range []string{"id", "email", "name", "role"} {
		if _, ok := user[k]; !ok {
			t.Fatalf("user block missing %q: %v", k, user)
		}
	}
	if user["email"] != "sso@example.com" || user["role"] != db.RoleUser {
		t.Fatalf("user block = %v", user)
	}
}

// TestSSOCodeIsOneShotOverHTTP — a replayed code must 404, so a code captured
// from browser history or a proxy log after the fact is worthless.
func TestSSOCodeIsOneShotOverHTTP(t *testing.T) {
	f := newSSOFixture(t, []string{ssoHub})
	_, body := f.mint(t, ssoReturnURL)
	code, _ := body["code"].(string)

	if status, _ := f.redeem(t, code); status != http.StatusOK {
		t.Fatalf("first redeem status = %d, want 200", status)
	}
	status, body := f.redeem(t, code)
	if status != http.StatusNotFound {
		t.Fatalf("replay status = %d, want 404", status)
	}
	if strings.Contains(strings.ToLower(jsonStr(body)), "used") {
		t.Fatalf("replay response distinguishes 'already used': %v", body)
	}
}

// TestSSORedeemFailuresAreIndistinguishable — unknown, replayed, malformed,
// and disabled-user all produce byte-identical 404 bodies. Anything else turns
// this endpoint into an oracle.
func TestSSORedeemFailuresAreIndistinguishable(t *testing.T) {
	f := newSSOFixture(t, []string{ssoHub})

	_, body := f.mint(t, ssoReturnURL)
	spent, _ := body["code"].(string)
	if status, _ := f.redeem(t, spent); status != http.StatusOK {
		t.Fatalf("priming redeem failed with %d", status)
	}

	// A code belonging to a user who gets disabled between mint and redeem.
	_, body = f.mint(t, ssoReturnURL)
	disabledCode, _ := body["code"].(string)
	if _, err := f.store.ExecContext(context.Background(),
		`UPDATE users SET disabled = 1 WHERE id = ?`, f.userID); err != nil {
		t.Fatalf("disable user: %v", err)
	}

	cases := map[string]string{
		"already used":  spent,
		"unknown":       "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"empty":         "",
		"garbage":       "not a real code at all",
		"disabled user": disabledCode,
	}
	var seen string
	for name, code := range cases {
		status, body := f.redeem(t, code)
		if status != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404 (body %v)", name, status, body)
		}
		s := jsonStr(body)
		if seen == "" {
			seen = s
			continue
		}
		if s != seen {
			t.Fatalf("%s: body %q differs from %q — the endpoint is an oracle", name, s, seen)
		}
	}
}

// TestSSOMintRejectsForeignOrigins is the open-redirect guard at the HTTP
// layer, and checks that the refusal does NOT echo the submitted URL back.
func TestSSOMintRejectsForeignOrigins(t *testing.T) {
	f := newSSOFixture(t, []string{ssoHub})

	for _, bad := range []string{
		"https://hub.novadiffusion.com.evil.com/sso",
		"https://evil.com/sso",
		"http://hub.novadiffusion.com/sso",
		"https://hub.novadiffusion.com@evil.com/sso",
		"javascript:alert(document.domain)",
		"//evil.com/sso",
		"",
	} {
		t.Run(bad, func(t *testing.T) {
			status, body := f.mint(t, bad)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %v)", status, body)
			}
			if _, ok := body["code"]; ok {
				t.Fatalf("a code was minted for %q", bad)
			}
			// The rejected URL must not be reflected: an error body that
			// echoes attacker text is a small XSS surface wherever it is
			// rendered, and a confirmation oracle for probing the allowlist.
			if bad != "" && strings.Contains(jsonStr(body), bad) {
				t.Fatalf("error body echoes the submitted URL: %v", body)
			}
			if strings.Contains(jsonStr(body), "evil.com") {
				t.Fatalf("error body leaks attacker-controlled text: %v", body)
			}
		})
	}
}

// TestSSOMintRequiresAuth — the code IS a session, so only the holder of that
// session may ask for one.
func TestSSOMintRequiresAuth(t *testing.T) {
	f := newSSOFixture(t, []string{ssoHub})
	status, _ := f.post(t, "/api/v2/auth/sso/code", gin.H{"return_url": ssoReturnURL}, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated mint status = %d, want 401", status)
	}
	status, _ = f.post(t, "/api/v2/auth/sso/code", gin.H{"return_url": ssoReturnURL},
		map[string]string{"Authorization": "Bearer garbage"})
	if status != http.StatusUnauthorized {
		t.Fatalf("bad-token mint status = %d, want 401", status)
	}
}

// TestSSORedeemRequiresServiceToken — the redeem route is machine-only and
// must not be reachable with a user JWT.
func TestSSORedeemRequiresServiceToken(t *testing.T) {
	f := newSSOFixture(t, []string{ssoHub})
	_, body := f.mint(t, ssoReturnURL)
	code, _ := body["code"].(string)

	status, _ := f.post(t, "/api/v2/svc/sso/redeem", gin.H{"code": code}, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("no-token redeem status = %d, want 401", status)
	}
	status, _ = f.post(t, "/api/v2/svc/sso/redeem", gin.H{"code": code},
		map[string]string{"Authorization": "Bearer " + f.jwt})
	if status != http.StatusUnauthorized {
		t.Fatalf("user-JWT redeem status = %d, want 401", status)
	}
	// Still spendable — the rejected attempts must not have consumed it.
	if status, _ := f.redeem(t, code); status != http.StatusOK {
		t.Fatalf("code was consumed by a rejected request (status %d)", status)
	}
}

// TestSSOMintNotMountedWithoutOrigins — with no allowlist the route does not
// exist at all, rather than existing and rejecting.
func TestSSOMintNotMountedWithoutOrigins(t *testing.T) {
	f := newSSOFixture(t, nil)
	status, _ := f.mint(t, ssoReturnURL)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route should not be mounted)", status)
	}
}

// TestSSOMintRateLimited — 20/min per user, then 429.
func TestSSOMintRateLimited(t *testing.T) {
	f := newSSOFixture(t, []string{ssoHub})
	for i := 0; i < ssoCodeRPM; i++ {
		if status, body := f.mint(t, ssoReturnURL); status != http.StatusOK {
			t.Fatalf("mint %d status = %d, want 200 (body %v)", i+1, status, body)
		}
	}
	if status, _ := f.mint(t, ssoReturnURL); status != http.StatusTooManyRequests {
		t.Fatalf("mint %d status = %d, want 429", ssoCodeRPM+1, status)
	}
}

// TestSSOConcurrentRedeemOverHTTP — 16 simultaneous redeems of one code, one
// winner, through the full handler stack.
func TestSSOConcurrentRedeemOverHTTP(t *testing.T) {
	f := newSSOFixture(t, []string{ssoHub})
	_, body := f.mint(t, ssoReturnURL)
	code, _ := body["code"].(string)

	const n = 16
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		wins  int
		other []int
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/api/v2/svc/sso/redeem",
				strings.NewReader(`{"code":"`+code+`"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Service-Token", testSvcToken)
			rec := httptest.NewRecorder()
			f.engine.ServeHTTP(rec, req)
			mu.Lock()
			defer mu.Unlock()
			switch rec.Code {
			case http.StatusOK:
				wins++
			case http.StatusNotFound:
			default:
				other = append(other, rec.Code)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected statuses: %v", other)
	}
	if wins != 1 {
		t.Fatalf("%d of %d concurrent redeems succeeded, want exactly 1", wins, n)
	}
}

func jsonStr(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestSSOMintRejectsCodeParamAndFragment — the allowlist pins the ORIGIN of
// return_url and nothing else, so its query string is attacker-controlled even
// when the origin is legitimate.
//
// The attack this closes: a registered user mints a code for themselves, then
// sends a victim to /login?return=https://hub.novadiffusion.com/sso?code=<their
// own code>. If the destination URL is assembled by appending "&code=<victim's
// code>", the far side reads URLSearchParams.get("code") — the FIRST one — and
// signs the victim's browser into the sibling product as the ATTACKER: work the
// victim does lands in the attacker's account, spend lands on the attacker's
// wallet. A "#" is the same trick from the other side, hiding the real code in
// a fragment the destination never sends anywhere.
//
// The client now sets the parameter through the URL API (which replaces rather
// than appends); this is the server-side half, so the exploit needs two
// independent regressions rather than one.
func TestSSOMintRejectsCodeParamAndFragment(t *testing.T) {
	f := newSSOFixture(t, []string{ssoHub})

	for _, bad := range []string{
		ssoHub + "/sso?code=attacker-owned-code",
		ssoHub + "/sso?next=x&code=attacker-owned-code",
		ssoHub + "/sso?code=",
		ssoHub + "/sso#",
		ssoHub + "/sso#/deep",
		ssoHub + "/sso?code=attacker#",
		// Over the length bound: the origin is fine, but the path is not
		// something we let an authenticated caller write into saas.db.
		ssoHub + "/sso?pad=" + strings.Repeat("A", maxReturnURLLen),
	} {
		t.Run(bad, func(t *testing.T) {
			status, body := f.mint(t, bad)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %v)", status, body)
			}
			if _, ok := body["code"]; ok {
				t.Fatalf("a code was minted for %q", bad)
			}
			if strings.Contains(jsonStr(body), "attacker-owned-code") {
				t.Fatalf("error body echoes the submitted URL: %v", body)
			}
		})
	}

	// ...while an ordinary query on an allowed origin still works: the gate is
	// about the `code` parameter, not about queries in general.
	status, body := f.mint(t, ssoHub+"/sso?next=%2Fapp%2Fgallery")
	if status != http.StatusOK {
		t.Fatalf("benign query rejected: status = %d body = %v", status, body)
	}
	if got, _ := body["return_url"].(string); got != ssoHub+"/sso?next=%2Fapp%2Fgallery" {
		t.Fatalf("return_url = %q, want the URL echoed verbatim", got)
	}
}
