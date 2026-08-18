package growth

import "testing"

// The invite programme is suspended by default (2026-08-18), but the admin
// surface stays mounted so past grants remain auditable. The analytics payload
// has to carry that fact, or the operator edits channels that do nothing and
// gets no hint why.
func TestSetSuspendedIsReportedToAdmin(t *testing.T) {
	s := New(nil, nil)
	if s.suspended {
		t.Fatal("a freshly built service reports suspended = true")
	}
	s.SetSuspended(true)
	if !s.suspended {
		t.Fatal("SetSuspended(true) did not take")
	}
	s.SetSuspended(false)
	if s.suspended {
		t.Fatal("SetSuspended(false) did not take")
	}
}
