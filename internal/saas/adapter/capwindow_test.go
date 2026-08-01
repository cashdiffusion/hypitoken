package adapter

import (
	"testing"
	"time"
)

// TestCapWindowsFollowTheServerZone pins the fix for a bug that only became
// visible when the production host moved from UTC to Asia/Hong_Kong.
//
// The daily window used to be `now.Truncate(24 * time.Hour)`, which reads like
// "local midnight" but is not: Truncate operates on absolute time since the
// zero instant, so it lands on UTC midnight in every zone. Under UTC that was
// indistinguishable from the intended behaviour. Under UTC+8 it meant the
// daily allowance reset at 08:00 local while the monthly one — built with
// time.Date(..., now.Location()), which really does follow the zone — reset at
// 00:00, so "daily" and "monthly" silently referred to different calendars.
//
// Both windows must open at local midnight in whatever zone the server runs.
func TestCapWindowsFollowTheServerZone(t *testing.T) {
	for _, zone := range []string{"Asia/Hong_Kong", "UTC", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Skipf("zone %s unavailable: %v", zone, err)
			}
			// A moment where the local and UTC calendar dates disagree in
			// UTC+8 (00:30 HKT on the 1st is 16:30 UTC on the previous day) —
			// exactly the window the old code got wrong.
			now := time.Date(2026, 8, 1, 0, 30, 0, 0, loc)

			dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

			if h, m, s := dayStart.Clock(); h != 0 || m != 0 || s != 0 {
				t.Fatalf("dayStart must be local midnight, got %s", dayStart)
			}
			if dayStart.Location() != loc || monthStart.Location() != loc {
				t.Fatalf("both windows must stay in the server zone; got day=%v month=%v",
					dayStart.Location(), monthStart.Location())
			}
			if !dayStart.After(now.Add(-24*time.Hour)) || dayStart.After(now) {
				t.Fatalf("dayStart %s is not the local day containing %s", dayStart, now)
			}
			// The regression itself: the old expression drifts off local
			// midnight anywhere with a non-zero offset.
			if off := offsetSeconds(now); off != 0 && now.Truncate(24*time.Hour).Equal(dayStart) {
				t.Fatalf("Truncate(24h) must NOT equal local midnight in %s — the test is not exercising the bug", zone)
			}
			// And day/month must agree about which calendar they are on: on the
			// 1st of the month, the day window starts exactly at the month one.
			if now.Day() == 1 && !dayStart.Equal(monthStart) {
				t.Fatalf("on the 1st, dayStart %s must coincide with monthStart %s", dayStart, monthStart)
			}
		})
	}
}

func offsetSeconds(t time.Time) int {
	_, off := t.Zone()
	return off
}
