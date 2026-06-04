package server

import (
	"testing"
	"time"

	"github.com/wjsoj/cc-core/auth"
)

// isCodexRetryableStatus decides whether an upstream status from a Codex
// API-key relay is rotated away from (429 / 5xx) or forwarded to the client
// (every other 4xx). Lock the classification down so a future tweak can't
// silently start retrying client-fault 4xx or stop retrying gateway 5xx.
func TestIsCodexRetryableStatus(t *testing.T) {
	for _, s := range []int{429, 500, 502, 503, 504, 529, 599} {
		if !isCodexRetryableStatus(s) {
			t.Errorf("status %d should be retryable (rotate to next credential)", s)
		}
	}
	for _, s := range []int{200, 201, 400, 401, 403, 404, 409, 422} {
		if isCodexRetryableStatus(s) {
			t.Errorf("status %d should be forwarded to the client, not retried", s)
		}
	}
}

// Regression for the dead-relay 502 incident: a Codex API-key relay returning
// 5xx must be taken out of rotation. MarkFailure alone never downgrades an API
// key — IsHealthy() skips the consecutive-failure "degraded" heuristic for
// KindAPIKey — so cooldownCodexAPIKey has to apply a quota cooldown, which is
// the one lever IsHealthy() honors for API keys.
func TestCooldownCodexAPIKey5xxSidelinesRelay(t *testing.T) {
	s := &Server{}
	a := &auth.Auth{ID: "relay", Kind: auth.KindAPIKey, Provider: auth.ProviderOpenAI}

	if !a.IsHealthy() {
		t.Fatal("a fresh API-key credential should be healthy")
	}

	s.cooldownCodexAPIKey(a, 502, time.Time{})

	if a.IsHealthy() {
		t.Fatal("relay should be unhealthy (cooled down) after a 5xx, so Acquire skips it")
	}
	info := a.Snapshot()
	if info.QuotaResetAt.IsZero() {
		t.Fatal("expected a bounded cooldown (QuotaResetAt set), got an open-ended one")
	}
	if d := time.Until(info.QuotaResetAt); d <= 0 || d > codexAPIKey5xxCooldown+time.Second {
		t.Fatalf("cooldown window out of range: %v (want ~%v)", d, codexAPIKey5xxCooldown)
	}
}
