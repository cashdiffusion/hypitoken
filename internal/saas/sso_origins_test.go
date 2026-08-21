package saas

import "testing"

const hubOrigin = "https://hub.novadiffusion.com"

func mustAllowlist(t *testing.T, entries ...string) *OriginAllowlist {
	t.Helper()
	a, err := NewOriginAllowlist(entries)
	if err != nil {
		t.Fatalf("NewOriginAllowlist(%v): %v", entries, err)
	}
	return a
}

// TestOriginAllowlistExactMatchOnly is the open-redirect test. Every case
// below is a real technique for smuggling an attacker origin past a sloppy
// allowlist; each one must be refused, because the code minted for an allowed
// return_url IS the user's session.
func TestOriginAllowlistExactMatchOnly(t *testing.T) {
	a := mustAllowlist(t, hubOrigin)

	allowed := []string{
		hubOrigin,
		hubOrigin + "/",
		hubOrigin + "/sso",
		hubOrigin + "/sso?code=x#frag",
		"HTTPS://HUB.NOVADIFFUSION.COM/sso",     // scheme + host are case-insensitive
		"https://hub.novadiffusion.com:443/sso", // default port is not a different origin
	}
	for _, u := range allowed {
		t.Run("allow/"+u, func(t *testing.T) {
			got, ok := a.Allows(u)
			if !ok {
				t.Fatalf("%q was rejected but is the allowed origin", u)
			}
			if got != u {
				t.Fatalf("returned %q, want the submitted URL %q", got, u)
			}
		})
	}

	denied := []string{
		"https://hub.novadiffusion.com.evil.com/sso", // suffix-append: beats startsWith
		"https://evil.com/hub.novadiffusion.com",     // origin in the path
		"https://hub.novadiffusion.com@evil.com/sso", // userinfo smuggling
		"https://evil.com#https://hub.novadiffusion.com",
		"http://hub.novadiffusion.com/sso",       // scheme downgrade
		"https://hub.novadiffusion.com:8443/sso", // different port is a different origin
		"https://sub.hub.novadiffusion.com/sso",  // no implicit subdomains
		"https://novadiffusion.com/sso",          // no parent domain
		"//hub.novadiffusion.com/sso",            // protocol-relative
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"/sso",
		"",
		"   ",
		"https://hüb.novadiffusion.com/sso", // homograph
	}
	for _, u := range denied {
		t.Run("deny/"+u, func(t *testing.T) {
			if got, ok := a.Allows(u); ok {
				t.Fatalf("%q was ALLOWED (returned %q) — open redirect", u, got)
			}
		})
	}
}

// TestOriginAllowlistEmptyIsOff — an unconfigured allowlist must deny, never
// fail open, and must report itself as absent so the route is not mounted.
func TestOriginAllowlistEmptyIsOff(t *testing.T) {
	for _, entries := range [][]string{nil, {}, {""}, {"  ", ""}} {
		a, err := NewOriginAllowlist(entries)
		if err != nil {
			t.Fatalf("NewOriginAllowlist(%v): %v", entries, err)
		}
		if a != nil {
			t.Fatalf("NewOriginAllowlist(%v) = %v, want nil (feature off)", entries, a)
		}
		if a.Len() != 0 {
			t.Fatalf("nil allowlist Len() = %d, want 0", a.Len())
		}
		if _, ok := a.Allows(hubOrigin); ok {
			t.Fatal("nil allowlist allowed an origin — failing open")
		}
	}
}

// TestOriginAllowlistRejectsMalformed — a bad entry is a boot-time error, not
// a silently-dropped line. A quietly-shortened allowlist looks identical to a
// working one until the day it matters.
func TestOriginAllowlistRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"*.novadiffusion.com",
		"https://*.novadiffusion.com",
		"hub.novadiffusion.com",                 // no scheme
		"ftp://hub.novadiffusion.com",           // not http(s)
		"https://",                              // no host
		"https://hub.novadiffusion.com/sso",     // path is not part of an origin
		"https://hub.novadiffusion.com?x=1",     // nor a query
		"https://hub.novadiffusion.com#f",       // nor a fragment
		"https://user:pw@hub.novadiffusion.com", // userinfo
		"javascript:alert(1)",
	} {
		t.Run(bad, func(t *testing.T) {
			if a, err := NewOriginAllowlist([]string{bad}); err == nil {
				t.Fatalf("accepted malformed entry %q (allowlist=%v)", bad, a.Origins())
			}
		})
	}
}

// TestOriginAllowlistNormalizesAndDedupes — entries that mean the same origin
// collapse to one, so Origins() is a faithful picture of what is allowed.
func TestOriginAllowlistNormalizes(t *testing.T) {
	a := mustAllowlist(t,
		"HTTPS://HUB.NOVADIFFUSION.COM",
		"https://hub.novadiffusion.com:443",
		"https://hub.novadiffusion.com/",
		"http://localhost:8330",
		"http://localhost:80",
	)
	got := a.Origins()
	want := []string{hubOrigin, "http://localhost:8330", "http://localhost"}
	if len(got) != len(want) {
		t.Fatalf("origins = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("origins = %v, want %v", got, want)
		}
	}
	// Origins() must hand back a copy — a caller mutating it must not be able
	// to edit the allowlist.
	got[0] = "https://evil.com"
	if _, ok := a.Allows("https://evil.com/"); ok {
		t.Fatal("mutating the Origins() slice changed the allowlist")
	}
	if _, ok := a.Allows(hubOrigin + "/sso"); !ok {
		t.Fatal("mutating the Origins() slice removed a real entry")
	}
}

// TestOriginAllowlistMultipleEntries — each listed origin is independently
// allowed, and nothing else is.
func TestOriginAllowlistMultipleEntries(t *testing.T) {
	a := mustAllowlist(t, hubOrigin, "http://localhost:8330")
	for _, u := range []string{hubOrigin + "/sso", "http://localhost:8330/sso"} {
		if _, ok := a.Allows(u); !ok {
			t.Fatalf("%q rejected", u)
		}
	}
	if _, ok := a.Allows("http://localhost:5174/sso"); ok {
		t.Fatal("an unlisted port was allowed")
	}
}
