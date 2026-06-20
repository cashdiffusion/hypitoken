package arena

import (
	"sync"
	"testing"
	"time"
)

func TestHubPublishSubscribe(t *testing.T) {
	h := NewHub(5)
	id, ch := h.Subscribe()
	defer h.Unsubscribe(id)

	h.Publish(Event{ActorID: "a1", Tokens: 10})
	select {
	case e := <-ch:
		if e.ActorID != "a1" || e.Tokens != 10 {
			t.Fatalf("got %+v", e)
		}
	default:
		t.Fatal("expected an event")
	}
}

func TestHubRingReplay(t *testing.T) {
	h := NewHub(3)
	for i := 0; i < 5; i++ {
		h.Publish(Event{ActorID: "x", Tokens: int64(i)})
	}
	recent := h.Recent()
	if len(recent) != 3 {
		t.Fatalf("ring size: want 3, got %d", len(recent))
	}
	// Oldest-first: should be tokens 2,3,4 (last 3).
	if recent[0].Tokens != 2 || recent[2].Tokens != 4 {
		t.Fatalf("ring contents: %+v", recent)
	}
}

func TestHubNonBlockingDrop(t *testing.T) {
	h := NewHub(2)
	// Subscribe but never drain — the 64-buffer then overflow must not block.
	id, _ := h.Subscribe()
	defer h.Unsubscribe(id)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			h.Publish(Event{ActorID: "flood", Tokens: int64(i)})
		}
		close(done)
	}()
	select {
	case <-done: // publisher finished despite a stalled subscriber → drops work
	case <-time.After(2 * time.Second):
		t.Fatal("publisher blocked on a stalled subscriber")
	}
}

func TestHubConcurrentPublish(_ *testing.T) {
	h := NewHub(10)
	id, ch := h.Subscribe()
	defer h.Unsubscribe(id)
	go func() {
		for e := range ch {
			_ = e // drain; body is non-empty to satisfy linters
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h.Publish(Event{ActorID: "c", Tokens: int64(n)})
			}
		}(i)
	}
	wg.Wait()
}

func TestPseudonymStable(t *testing.T) {
	a := pseudonymFor(42)
	b := pseudonymFor(42)
	if a != b {
		t.Fatalf("pseudonym not stable: %q vs %q", a, b)
	}
	if a == "" {
		t.Fatal("empty pseudonym")
	}
}

func TestDisplayName(t *testing.T) {
	// Opted in with a nickname → real name.
	if got := displayName(7, "Cool Dev", true); got != "Cool Dev" {
		t.Fatalf("opted-in name: got %q", got)
	}
	// Opted out → pseudonym, never the real nickname.
	got := displayName(7, "Cool Dev", false)
	if got == "Cool Dev" {
		t.Fatal("opted-out leaked the real nickname")
	}
	if got != pseudonymFor(7) {
		t.Fatalf("opted-out should be the stable pseudonym, got %q", got)
	}
	// Opted in but empty nickname → falls back to pseudonym.
	if got := displayName(7, "", true); got != pseudonymFor(7) {
		t.Fatalf("empty opted-in name: got %q", got)
	}
}

func TestActorIDStableAndOpaque(t *testing.T) {
	a := actorIDFor(123)
	if a != actorIDFor(123) {
		t.Fatal("actor id not stable")
	}
	if a == "" {
		t.Fatal("empty actor id")
	}
	// Must not be the raw id.
	if a == "123" {
		t.Fatal("actor id leaked the raw user id")
	}
}
