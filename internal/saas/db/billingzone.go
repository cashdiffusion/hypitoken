package db

import "time"

// BillingZone is the calendar every spending window is measured in: the day a
// daily cap covers, the month a monthly cap covers, and the "spent today"
// figure the dashboard shows against them.
//
// It is a fixed UTC+8 offset rather than time.Local on purpose. Reading the
// host's zone makes billing windows a property of machine configuration:
// moving the service to another host, or an operator running timedatectl,
// would silently redraw the boundary a customer's allowance resets on. Pinning
// it means the windows are the same everywhere the code runs — in production,
// on a laptop, and in CI — so a cap test cannot pass only because the test
// machine happens to sit in the right zone.
//
// UTC+8 with no DST, so a fixed offset is exact; there is no need to depend on
// the tzdata database being present in the image. CPA-Claude pins the same
// offset (internal/saas/db/workspace.go), and the two forks must agree — they
// bill the same customers off the same calendar.
//
// Note this is deliberately narrower than the host timezone, which is
// Asia/Hong_Kong so that logs, the admin panel, and finance CSV exports read
// in local time. Presentation follows the host; money does not.
var BillingZone = time.FixedZone("UTC+8", 8*3600)

// BillingDayStart returns the instant the billing day containing now began.
func BillingDayStart(now time.Time) time.Time {
	t := now.In(BillingZone)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, BillingZone)
}

// BillingMonthStart returns the instant the billing month containing now began.
func BillingMonthStart(now time.Time) time.Time {
	t := now.In(BillingZone)
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, BillingZone)
}
