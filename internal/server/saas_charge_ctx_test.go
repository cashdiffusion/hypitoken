package server

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// A wallet debit is the last step of a turn we have ALREADY paid the upstream
// for. Tying it to the caller still being connected meant every client
// disconnect aborted the DB write and dropped the charge — production logged
// 14865 of those over five days, across many distinct paying users.
func TestChargeCtxSurvivesClientDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	canceled, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil).WithContext(canceled)
	cancel() // the client hung up

	if c.Request.Context().Err() == nil {
		t.Fatal("precondition: the request context should already be canceled")
	}
	ctx := chargeCtx(c)
	if err := ctx.Err(); err != nil {
		t.Errorf("chargeCtx must outlive the request, got %v — this is the bug that dropped the charges", err)
	}
	select {
	case <-ctx.Done():
		t.Error("chargeCtx must not be Done when the caller disconnects")
	default:
	}
}

// It must still carry the request's values, so anything keyed off the request
// (trace ids, tenant lookups) keeps working on the settle path.
func TestChargeCtxKeepsRequestValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	type ctxKey struct{}
	base := context.WithValue(context.Background(), ctxKey{}, "tenant-7")
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil).WithContext(base)

	if got := chargeCtx(c).Value(ctxKey{}); got != "tenant-7" {
		t.Errorf("request values lost: got %v, want tenant-7", got)
	}
}

// A deadline on the request must not survive either: the debit can legitimately
// outlast the response it belongs to.
func TestChargeCtxDropsRequestDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	base, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil).WithContext(base)
	time.Sleep(time.Millisecond)

	if _, ok := chargeCtx(c).Deadline(); ok {
		t.Error("chargeCtx should not inherit the request's deadline")
	}
	if err := chargeCtx(c).Err(); err != nil {
		t.Errorf("an expired request deadline must not kill the debit, got %v", err)
	}
}

// Defensive: the helper is called from paths that can in principle see a bare
// context, and losing a charge to a nil-pointer panic would be worse than the
// bug it replaces.
func TestChargeCtxHandlesMissingRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = nil
	if ctx := chargeCtx(c); ctx == nil || ctx.Err() != nil {
		t.Errorf("chargeCtx(nil request) = %v, want a usable context", ctx)
	}
	if ctx := chargeCtx(nil); ctx == nil || ctx.Err() != nil {
		t.Errorf("chargeCtx(nil) = %v, want a usable context", ctx)
	}
}
