package billing

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// stubGateway is a Gateway that records nothing and settles nothing. Not
// MockGateway: that one fires AutoConfirm in a goroutine and would credit the
// wallet two seconds into the test, which is not what is under examination
// here.
type stubGateway struct{ lastCNY float64 }

func (g *stubGateway) CreatePayment(_ context.Context, p PayParams) (*PayResult, error) {
	g.lastCNY = p.TotalCNY
	return &PayResult{QRCode: "stub://" + p.OutTradeNo}, nil
}
func (g *stubGateway) VerifyNotify(map[string][]string) (*Notification, error) {
	return nil, errors.New("stub")
}
func (g *stubGateway) QueryTrade(context.Context, string) (*Notification, error) {
	return nil, errors.New("stub")
}
func (g *stubGateway) AppID() string { return "stub-app" }

// newAmountHandler builds a real Handler over a real (temp) saas.db with one
// real user, and a router that authenticates as that user.
func newAmountHandler(t *testing.T) (*Handler, *gin.Engine, *db.User, *stubGateway) {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatalf("open saas.db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	grp, err := store.DefaultGroup(context.Background())
	if err != nil {
		t.Fatalf("default group: %v", err)
	}
	u, err := store.CreateUser(context.Background(), "buyer@example.test", "x", "user", grp.ID, true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	gw := &stubGateway{}
	h := NewHandler(store, NewRate("", 7.2), gw, "Test")
	h.SiteURL = testSite

	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.POST("/topup", func(c *gin.Context) {
		c.Set(string(saasauth.CtxUser), u)
		h.topup(c)
	})
	return h, e, u, gw
}

func postAmount(t *testing.T, e *gin.Engine, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/topup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	e.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// TestTopupCreditsExactlyWhatIsCharged is the money-conservation invariant for
// the one path that puts money IN.
//
// The wallet is credited the order row's usd_credit. Every rail is charged a
// whole number of minor units: Stripe gets minorUnits(usd), the QR rail gets
// round2(usd * rate). Before this was pinned, usd_credit kept the caller's raw
// float — so $1.004 was charged 100 minor units ($1.00) and credited $1.004,
// and verifySessionForOrder's `amount_total >= minorUnits(credit)` check passed
// on exactly that gap because the gap is smaller than the unit it compares in.
//
// Half a cent an order is not a heist. "Credited more than charged" is still
// not a property a wallet may have, and the 2026-08-18 audit's 1:1
// reconciliation only means something while the two are the same number.
func TestTopupCreditsExactlyWhatIsCharged(t *testing.T) {
	h, e, u, _ := newAmountHandler(t)

	// Amounts a client can send that are not whole cents. The first two are
	// the ones that used to credit MORE than they charged.
	for _, in := range []string{"1.004", "999.994", "10.0049999", "25.005", "1.0000000000000002", "50"} {
		code, body := postAmount(t, e, `{"usd":`+in+`}`)
		if code != http.StatusOK {
			t.Fatalf("usd=%s: status %d %v", in, code, body)
		}
		out, _ := body["out_trade_no"].(string)
		if out == "" {
			t.Fatalf("usd=%s: no order id in %v", in, body)
		}
		o, err := h.DB.GetOrder(context.Background(), out)
		if err != nil {
			t.Fatalf("usd=%s: get order %s: %v", in, out, err)
		}
		if o.UserID != u.ID {
			t.Fatalf("usd=%s: order belongs to user %d, want %d", in, o.UserID, u.ID)
		}
		// The invariant: the credited amount is a whole number of cents, so
		// it is exactly representable as the amount a PSP is asked to charge.
		charged := float64(minorUnits(o.USDCredit)) / 100
		if math.Abs(charged-o.USDCredit) > 1e-9 {
			t.Fatalf("usd=%s: wallet would be credited %.10f but the rail charges %.2f — "+
				"a top-up must never credit more than it charges", in, o.USDCredit, charged)
		}
		// And the response tells the buyer the same number that was stored.
		if got, _ := body["usd_credit"].(float64); math.Abs(got-o.USDCredit) > 1e-9 {
			t.Fatalf("usd=%s: response says %.10f, order row says %.10f", in, got, o.USDCredit)
		}
	}
}

// TestTopupBoundsApplyToTheCanonicalAmount: rounding happens before the $1 /
// $1000 gate, so the number the bounds are checked against is the number that
// is charged and credited. A value that rounds to $1.00 is charged $1.00.
func TestTopupBoundsApplyToTheCanonicalAmount(t *testing.T) {
	_, e, _, _ := newAmountHandler(t)
	for _, tc := range []struct {
		in   string
		code int
	}{
		{"0.994", http.StatusBadRequest}, // rounds to 0.99 — still under the floor
		{"1000.004", http.StatusOK},      // rounds to 1000.00 — exactly the ceiling
		{"1000.006", http.StatusBadRequest},
		{"0", http.StatusBadRequest},
		{"-5", http.StatusBadRequest},
	} {
		code, body := postAmount(t, e, `{"usd":`+tc.in+`}`)
		if code != tc.code {
			t.Fatalf("usd=%s: status %d, want %d (%v)", tc.in, code, tc.code, body)
		}
	}
}
