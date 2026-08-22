package billing

import (
	"testing"
	"time"
)

func newRateHandler() *Handler {
	return &Handler{createdAt: make(map[int64][]time.Time)}
}

// TestAllowCreate_DoesNotConsumeSlot is the regression that matters: a user
// who trips the max-pending guard must not also burn hourly rate budget, or
// the two limits compound into an hours-long payment lockout (2026-08-22:
// one customer's abandoned checkouts left them unable to top up at all).
func TestAllowCreate_DoesNotConsumeSlot(t *testing.T) {
	h := newRateHandler()
	for range maxOrdersPerHour * 2 {
		if ok, _ := h.allowCreate(7); !ok {
			t.Fatal("allowCreate denied without any noteCreate — check alone must not consume")
		}
	}
	if got := len(h.createdAt[7]); got != 0 {
		t.Fatalf("createdAt = %d stamps, want 0", got)
	}
}

func TestNoteCreate_EnforcesHourlyCeiling(t *testing.T) {
	h := newRateHandler()
	for i := range maxOrdersPerHour {
		if ok, _ := h.allowCreate(7); !ok {
			t.Fatalf("denied at order %d, want the first %d allowed", i+1, maxOrdersPerHour)
		}
		h.noteCreate(7)
	}
	ok, retry := h.allowCreate(7)
	if ok {
		t.Fatalf("order %d allowed, want deny", maxOrdersPerHour+1)
	}
	if retry < 1 || retry > 3600 {
		t.Fatalf("retry_after = %ds, want 1..3600", retry)
	}
	// A different user is unaffected.
	if ok, _ := h.allowCreate(8); !ok {
		t.Fatal("user 8 denied by user 7's budget")
	}
}

// Stamps older than the window age out, so the ceiling is a rolling hour and
// never a permanent ban.
func TestRateWindow_Slides(t *testing.T) {
	h := newRateHandler()
	old := time.Now().Add(-2 * time.Hour)
	for range maxOrdersPerHour {
		h.createdAt[7] = append(h.createdAt[7], old)
	}
	if ok, _ := h.allowCreate(7); !ok {
		t.Fatal("denied on stamps that aged out of the 1h window")
	}
	if got := len(h.createdAt[7]); got != 0 {
		t.Fatalf("stale stamps left: %d, want pruned to 0", got)
	}
}
