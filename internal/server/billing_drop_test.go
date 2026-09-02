package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBillingDroppedMarker(t *testing.T) {
	if got := billingDropped(nil); got != "" {
		t.Fatalf("nil error must produce no marker, got %q", got)
	}
	got := billingDropped(errors.New("database is locked"))
	if !strings.HasPrefix(got, "billing dropped: ") || !strings.Contains(got, "database is locked") {
		t.Fatalf("marker = %q", got)
	}
	if joinLogError("", got) != got {
		t.Fatal("joining onto an empty field must yield the marker alone")
	}
	if j := joinLogError("stream truncated before terminal event", got); !strings.HasPrefix(j, "stream truncated") || !strings.HasSuffix(j, got) {
		t.Fatalf("both conditions must survive the join: %q", j)
	}
	if joinLogError("x", "") != "x" {
		t.Fatal("an empty addition must not alter the field")
	}
}

// TestChargeCtxSlotsAreDistinctWithinOneRequest: the main charge, an advisor
// sub-call and a WS turn on one request must never share a key — a replay of
// one would otherwise swallow the others — while all of them share the
// request's base id, and a second request gets a different base.
func TestChargeCtxSlotsAreDistinctWithinOneRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newCtx := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		return c
	}
	c := newCtx()
	main1 := ChargeIdemKey(chargeCtx(c))
	main2 := ChargeIdemKey(chargeCtx(c))
	advisor := ChargeIdemKey(chargeCtxSlot(c, "advisor:claude-opus-5"))
	turn3 := ChargeIdemKey(chargeCtxSlot(c, "turn:3"))

	if main1 == "" || !strings.HasPrefix(main1, "charge:") || !strings.HasSuffix(main1, ":main") {
		t.Fatalf("main key = %q", main1)
	}
	if main1 != main2 {
		t.Fatalf("the same slot on the same request must yield the same key: %q vs %q", main1, main2)
	}
	if advisor == main1 || turn3 == main1 || advisor == turn3 {
		t.Fatalf("slots collided: main=%q advisor=%q turn=%q", main1, advisor, turn3)
	}
	base := strings.TrimSuffix(main1, ":main")
	if !strings.HasPrefix(advisor, base) || !strings.HasPrefix(turn3, base) {
		t.Fatalf("slots must share the request base %q: advisor=%q turn=%q", base, advisor, turn3)
	}

	other := ChargeIdemKey(chargeCtx(newCtx()))
	if other == main1 {
		t.Fatal("two requests minted the same charge key")
	}

	if ChargeIdemKey(chargeCtx(nil)) != "" {
		t.Fatal("a nil gin context must carry no key")
	}
	if got := ChargeIdemKey(WithChargeIdemKey(chargeCtx(nil), "svc:job:1")); got != "svc:job:1" {
		t.Fatalf("WithChargeIdemKey = %q", got)
	}
}
