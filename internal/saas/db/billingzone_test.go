package db

import (
	"os"
	"testing"
	"time"
)

// Billing windows must be a property of the code, not of the machine. These
// assertions run under several process timezones, including the two the
// production host has actually been set to (UTC before 2026-08-01,
// Asia/Hong_Kong after) and one that is neither.
func withTZ(t *testing.T, tz string, fn func()) {
	t.Helper()
	prev, had := os.LookupEnv("TZ")
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("TZ", prev)
		} else {
			_ = os.Unsetenv("TZ")
		}
	})
	_ = os.Setenv("TZ", tz)
	fn()
}

// TestBillingWindowsArePinned covers the bug the production timezone switch
// exposed, and the decision taken afterwards.
//
// PreCheck's daily window used to be `now.Truncate(24 * time.Hour)`, which
// reads like "local midnight" but is not: Truncate operates on absolute time
// since the zero instant, so it always lands on UTC midnight. On a UTC host
// that was indistinguishable from the intent. On UTC+8 it would have reset the
// daily allowance at 08:00 while the monthly cap reset at 00:00 — two
// different calendars behind the words "daily" and "monthly".
//
// The fix pins both to BillingZone rather than following time.Local, so moving
// hosts or running timedatectl cannot redraw the boundary a customer's
// allowance resets on.
func TestBillingWindowsArePinned(t *testing.T) {
	// 2026-08-01 00:30 UTC+8 is 2026-07-31 16:30 UTC — the two calendars
	// disagree on both the day AND the month here, which is exactly where a
	// zone mistake surfaces.
	now := time.Date(2026, 7, 31, 16, 30, 0, 0, time.UTC)
	want := time.Date(2026, 8, 1, 0, 0, 0, 0, BillingZone)

	for _, tz := range []string{"UTC", "Asia/Hong_Kong", "America/New_York"} {
		t.Run(tz, func(t *testing.T) {
			withTZ(t, tz, func() {
				if got := BillingDayStart(now); !got.Equal(want) {
					t.Fatalf("BillingDayStart = %s, want %s (must not vary with the process timezone)",
						got.UTC(), want.UTC())
				}
				if got := BillingMonthStart(now); !got.Equal(want) {
					t.Fatalf("BillingMonthStart = %s, want %s (must not vary with the process timezone)",
						got.UTC(), want.UTC())
				}
			})
		})
	}
}

// The regression itself: the expression that was replaced disagrees with the
// billing day. If this ever stops holding, Truncate has changed semantics and
// the reasoning recorded in BillingZone's comment no longer applies.
func TestTruncate24hIsNotTheBillingDay(t *testing.T) {
	now := time.Date(2026, 7, 31, 16, 30, 0, 0, time.UTC)
	if now.Truncate(24 * time.Hour).Equal(BillingDayStart(now)) {
		t.Fatal("Truncate(24h) coincides with the billing day — this test no longer exercises the bug it guards")
	}
}

// Day and month must agree about which calendar they are on: on the 1st, the
// billing day opens exactly when the billing month does.
func TestBillingDayAndMonthShareOneCalendar(t *testing.T) {
	firstOfMonth := time.Date(2026, 7, 31, 20, 0, 0, 0, time.UTC) // = Aug 1, 04:00 UTC+8
	if d, m := BillingDayStart(firstOfMonth), BillingMonthStart(firstOfMonth); !d.Equal(m) {
		t.Fatalf("on the 1st the day window (%s) must coincide with the month window (%s)", d.UTC(), m.UTC())
	}
}

// The offset must match CPA-Claude's cstZone (internal/saas/db/workspace.go
// there): the two forks bill the same customers and must not disagree about
// when a day begins.
func TestBillingZoneIsUTCPlus8(t *testing.T) {
	_, off := time.Now().In(BillingZone).Zone()
	if off != 8*3600 {
		t.Fatalf("BillingZone offset = %ds, want %ds (UTC+8)", off, 8*3600)
	}
}
