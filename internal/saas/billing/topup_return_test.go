package billing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const (
	hubOrigin  = "https://hub.novadiffusion.com"
	hubReturn  = hubOrigin + "/app/billing"
	testOut    = "T20260822000000001"
	testSite   = "https://api.novadiffusion.com"
	evilOrigin = "https://hub.novadiffusion.com.evil.example"
)

// newReturnHandler builds the smallest Handler that exercises the return_url
// gate: SiteURL for the default path, and the allowlist under test.
func newReturnHandler(t *testing.T, origins []string) *Handler {
	t.Helper()
	allow, err := TopupReturnOriginsFrom(origins)
	if err != nil {
		t.Fatalf("TopupReturnOriginsFrom(%v): %v", origins, err)
	}
	return &Handler{SiteURL: testSite, TopupReturnOrigins: allow}
}

type returnResp struct {
	ReturnURL string `json:"return_url"`
	Error     string `json:"error"`
}

// postTopup drives the gate over a real HTTP round-trip, through exactly the
// two calls topup()/topupStripe() make: resolveTopupReturnURL to validate, then
// stripeReturnURLWith to build the value that goes into the response and into
// the Stripe Checkout Session. Stripe itself is not involved — the session
// create is a network call, and everything this change touches happens before
// it.
func postTopup(t *testing.T, h *Handler, provider, returnURL string) (int, returnResp) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.POST("/topup", func(c *gin.Context) {
		base, ok := h.resolveTopupReturnURL(c, provider, returnURL)
		if !ok {
			return // the 400 is already written
		}
		c.JSON(http.StatusOK, gin.H{"return_url": h.stripeReturnURLWith(base, testOut)})
	})
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/topup", nil))

	var body returnResp
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

// TestTopupReturnURL_AbsentIsUnchanged is the additive-only guarantee: with no
// return_url the response is byte-for-byte what it was before this change, and
// that holds whether or not the allowlist is configured.
func TestTopupReturnURL_AbsentIsUnchanged(t *testing.T) {
	for _, origins := range [][]string{nil, {hubOrigin}} {
		h := newReturnHandler(t, origins)
		want := h.stripeReturnURL(testOut)
		if want != testSite+"/app/billing?out="+testOut {
			t.Fatalf("baseline return URL = %q, unexpected shape", want)
		}
		code, body := postTopup(t, h, "stripe", "")
		if code != http.StatusOK {
			t.Fatalf("origins=%v: status %d, want 200", origins, code)
		}
		if body.ReturnURL != want {
			t.Fatalf("origins=%v: return_url = %q, want today's %q", origins, body.ReturnURL, want)
		}
	}
}

// TestTopupReturnURL_AllowlistedAccepted: the sibling's origin is honoured and
// comes back carrying the order id, which is the whole point — HypiHub needs to
// know which order to resume polling after an Alipay redirect.
func TestTopupReturnURL_AllowlistedAccepted(t *testing.T) {
	h := newReturnHandler(t, []string{hubOrigin})
	for _, in := range []string{
		hubReturn,
		hubReturn + "?tab=wallet",                   // caller's own query is preserved
		hubOrigin + ":443/app/billing",              // default port normalizes to the same origin
		"https://HUB.novadiffusion.com/app/billing", // host case-insensitive
	} {
		code, body := postTopup(t, h, "stripe", in)
		if code != http.StatusOK {
			t.Fatalf("%q: status %d (%s), want 200", in, code, body.Error)
		}
		if !strings.HasPrefix(body.ReturnURL, in) {
			t.Fatalf("%q: return_url = %q, want it to start with the supplied URL", in, body.ReturnURL)
		}
		if !strings.Contains(body.ReturnURL, "out="+testOut) {
			t.Fatalf("%q: return_url = %q, missing out=%s", in, body.ReturnURL, testOut)
		}
	}
	// The caller's existing query must be extended, not clobbered.
	_, body := postTopup(t, h, "stripe", hubReturn+"?tab=wallet")
	if body.ReturnURL != hubReturn+"?tab=wallet&out="+testOut {
		t.Fatalf("return_url = %q, want the out param appended with &", body.ReturnURL)
	}
}

// TestTopupReturnURL_Rejected covers every way in which a return_url must not
// be honoured. Each of these is an open redirect on a payment back-channel if
// the comparison ever loosens.
func TestTopupReturnURL_Rejected(t *testing.T) {
	h := newReturnHandler(t, []string{hubOrigin})
	cases := []struct{ name, url string }{
		{"unrelated origin", "https://evil.example/app/billing"},
		{"suffix-appended host", evilOrigin + "/app/billing"},
		{"prefix-appended host", "https://evilhub.novadiffusion.com/app/billing"},
		{"scheme differs", "http://hub.novadiffusion.com/app/billing"},
		{"port differs", "https://hub.novadiffusion.com:8443/app/billing"},
		{"trailing dot host", "https://hub.novadiffusion.com./app/billing"},
		{"subdomain of allowed host", "https://a.hub.novadiffusion.com/app/billing"},
		{"userinfo smuggling", "https://hub.novadiffusion.com@evil.example/app/billing"},
		{"fragment", hubReturn + "#done"},
		{"fragment only", hubOrigin + "#"},
		{"already carries out", hubReturn + "?out=T00000000000000000"},
		{"already carries session_id", hubReturn + "?session_id=cs_test_1"},
		{"protocol-relative", "//hub.novadiffusion.com/app/billing"},
		{"non-http scheme", "javascript:alert(1)"},
		{"over length", hubReturn + "?x=" + strings.Repeat("a", maxTopupReturnURLLen)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := postTopup(t, h, "stripe", tc.url)
			if code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400 (return_url=%q -> %q)", code, tc.url, body.ReturnURL)
			}
			if body.ReturnURL != "" {
				t.Fatalf("return_url = %q, want none", body.ReturnURL)
			}
			assertDoesNotEcho(t, body.Error, tc.url)
		})
	}
}

// TestTopupReturnURL_EmptyAllowlistRejects is the "no config, no new behaviour"
// half: a deployment that never set saas.topup_return_origins refuses the field
// outright rather than falling back to some default origin.
func TestTopupReturnURL_EmptyAllowlistRejects(t *testing.T) {
	for _, origins := range [][]string{nil, {}, {"  "}} {
		h := newReturnHandler(t, origins)
		if h.TopupReturnOrigins.Len() != 0 {
			t.Fatalf("origins=%v: allowlist should be empty", origins)
		}
		code, body := postTopup(t, h, "stripe", hubReturn)
		if code != http.StatusBadRequest {
			t.Fatalf("origins=%v: status %d, want 400", origins, code)
		}
		assertDoesNotEcho(t, body.Error, hubReturn)
	}
}

// TestTopupReturnURL_NonStripeRailRefuses: only the Stripe rail threads a
// return_url to the PSP. Accepting it on the QR rail and dropping it silently
// would tell the caller a redirect target was honoured when it was not.
func TestTopupReturnURL_NonStripeRailRefuses(t *testing.T) {
	h := newReturnHandler(t, []string{hubOrigin})
	for _, provider := range []string{"", "zpay", "alipay"} {
		code, _ := postTopup(t, h, provider, hubReturn)
		if code != http.StatusBadRequest {
			t.Fatalf("provider=%q: status %d, want 400", provider, code)
		}
		// ...and the same provider with no return_url is untouched.
		if code, _ := postTopup(t, h, provider, ""); code != http.StatusOK {
			t.Fatalf("provider=%q with no return_url: status %d, want 200", provider, code)
		}
	}
}

// TestTopupReturnURL_ShapeVerdictIsOriginIndependent mirrors the SSO path: the
// shape check runs before the origin check, so a malformed URL is refused with
// the same message whether or not its origin happens to be allowlisted. An
// error that varies by origin is an allowlist-probing oracle.
func TestTopupReturnURL_ShapeVerdictIsOriginIndependent(t *testing.T) {
	h := newReturnHandler(t, []string{hubOrigin})
	_, allowed := postTopup(t, h, "stripe", hubReturn+"#x")
	_, denied := postTopup(t, h, "stripe", "https://evil.example/app/billing#x")
	if allowed.Error != denied.Error {
		t.Fatalf("shape rejection differs by origin: %q vs %q", allowed.Error, denied.Error)
	}
}

// TestTopupReturnOriginsFrom_WildcardIsFatal: a wildcard must fail at boot, not
// widen the allowlist. The message names the key the operator has to fix.
func TestTopupReturnOriginsFrom_WildcardIsFatal(t *testing.T) {
	for _, bad := range []string{"*.novadiffusion.com", "https://*.novadiffusion.com", "https://hub.novadiffusion.com/app", "ftp://hub.novadiffusion.com", "hub.novadiffusion.com"} {
		if _, err := TopupReturnOriginsFrom([]string{bad}); err == nil {
			t.Fatalf("TopupReturnOriginsFrom(%q) accepted, want a boot-time error", bad)
		} else if !strings.Contains(err.Error(), "saas.topup_return_origins") {
			t.Fatalf("error %q does not name saas.topup_return_origins", err)
		}
	}
}

// assertDoesNotEcho pins the no-reflection rule: a rejected return_url is
// attacker-chosen text, so the error body must not repeat it (phishing surface
// for anything that renders the message, confirmation oracle for probing).
func assertDoesNotEcho(t *testing.T, msg, in string) {
	t.Helper()
	if msg == "" {
		t.Fatal("empty error message")
	}
	if strings.Contains(msg, in) {
		t.Fatalf("error %q echoes the rejected input", msg)
	}
	for _, frag := range []string{"evil", "novadiffusion", "javascript", "://"} {
		if strings.Contains(strings.ToLower(msg), frag) && strings.Contains(strings.ToLower(in), frag) {
			t.Fatalf("error %q leaks %q from the rejected input", msg, frag)
		}
	}
}
