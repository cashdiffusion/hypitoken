package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wjsoj/cc-core/auth"
)

// Two goroutines that both saw the stale bearer must produce ONE refresh: Codex
// refresh tokens rotate on use, and presenting the old one twice hard-fails the
// credential in cc-core.
func TestRecoverRejectedTokenRefreshesOnce(t *testing.T) {
	var gate refreshGate
	var mu sync.Mutex
	cur := "stale"
	var refreshes int32
	current := func() string { mu.Lock(); defer mu.Unlock(); return cur }
	refresh := func() error {
		atomic.AddInt32(&refreshes, 1)
		time.Sleep(20 * time.Millisecond) // let the second caller queue on the gate
		mu.Lock()
		cur = "fresh"
		mu.Unlock()
		return nil
	}
	var wg sync.WaitGroup
	results := make([]bool, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rotated, err := recoverRejectedToken(&gate, "cred", "stale", current, refresh)
			if err != nil {
				t.Errorf("caller %d: %v", i, err)
			}
			results[i] = rotated
		}(i)
	}
	wg.Wait()
	if n := atomic.LoadInt32(&refreshes); n != 1 {
		t.Fatalf("refresh ran %d times, want exactly 1", n)
	}
	for i, r := range results {
		if !r {
			t.Errorf("caller %d did not see a rotated token", i)
		}
	}
}

func TestRecoverRejectedTokenSkipsWhenAlreadyRotated(t *testing.T) {
	var gate refreshGate
	called := false
	rotated, err := recoverRejectedToken(&gate, "cred", "stale",
		func() string { return "fresh" },
		func() error { called = true; return nil })
	if err != nil || !rotated || called {
		t.Fatalf("rotated=%v err=%v refreshCalled=%v; want rotated, no error, no refresh", rotated, err, called)
	}
}

func TestRecoverRejectedTokenReportsRefreshFailure(t *testing.T) {
	var gate refreshGate
	boom := errors.New("invalid_grant")
	rotated, err := recoverRejectedToken(&gate, "cred", "stale",
		func() string { return "stale" },
		func() error { return boom })
	if !errors.Is(err, boom) || rotated {
		t.Fatalf("rotated=%v err=%v; want the refresh error and no rotation", rotated, err)
	}
}

// codex401Backend answers every turn with the body the ChatGPT backend sent on
// 2026-09-03 for a server-side-invalidated token.
func codex401Backend(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Provided authentication token is expired.","type":null,"code":"token_expired","param":null},"status":401}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The HTTP path used to answer a 401 with a bare one-minute cooldown: no
// strike, no refresh, and the credential back in rotation a minute later to
// fail again. It must now count the strike and re-mint the token.
func TestCodexOAuth401CountsStrikeAndForcesRefresh(t *testing.T) {
	backend := codex401Backend(t)
	cred := codexWSTestOAuth("codex-revoked")
	s := codexHTTPTestServer(backend.URL, cred)
	var refreshed []string
	s.codexRefresh = func(_ context.Context, a *auth.Auth) error {
		tok, _ := a.Credentials()
		refreshed = append(refreshed, tok)
		a.AccessToken = "re-minted" // what EnsureFresh does on success
		return nil
	}

	forwardCodexTurn(t, s, cred, "conv", "sk-tenant", "slot")

	if len(refreshed) != 1 || refreshed[0] != "oauth-token" {
		t.Fatalf("forced refresh calls = %v, want exactly one against the rejected bearer", refreshed)
	}
	if got := cred.Snapshot().Consecutive401s; got != 1 {
		t.Fatalf("Consecutive401s = %d, want 1 — the strike counter is what ends the loop", got)
	}
	if !cred.IsHealthy() {
		t.Fatal("credential was cooled down even though its token was re-minted; it should be back in rotation")
	}
}

// A refresh that cannot help (no rotation) keeps the old cooldown, and a
// sustained run of rejections retires the credential instead of cycling it
// through the cooldown forever.
func TestCodexOAuth401WithoutRotationCoolsDownThenHardFails(t *testing.T) {
	backend := codex401Backend(t)
	cred := codexWSTestOAuth("codex-revoked")
	s := codexHTTPTestServer(backend.URL, cred)
	s.codexRefresh = func(context.Context, *auth.Auth) error { return nil } // token unchanged

	forwardCodexTurn(t, s, cred, "conv", "sk-tenant", "slot")
	if cred.IsHealthy() {
		t.Fatal("a rejected bearer that could not be re-minted must cool the credential down")
	}
	if cred.IsHardFailed() {
		t.Fatal("one rejection must not hard-fail the credential (token-rotation races exist)")
	}
	for range 8 {
		forwardCodexTurn(t, s, cred, "conv", "sk-tenant", "slot")
	}
	if !cred.IsHardFailed() {
		t.Fatalf("after %d consecutive 401s the credential is still in rotation", cred.Snapshot().Consecutive401s+1)
	}
}
