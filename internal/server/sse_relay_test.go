package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/cc-core/usage"
)

func newClaudeStreamCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c, w
}

// failReader fails its first Read, simulating an upstream that resets the
// connection after the 200 but before any SSE event arrives.
type failReader struct{ err error }

func (f *failReader) Read(p []byte) (int, error) { return 0, f.err }

// A stream that breaks before emitting any event must NOT commit the response
// headers, so forwardWithFailover can still transparently retry on another
// credential.
func TestStreamSSENoCommitBeforeFirstByte(t *testing.T) {
	c, w := newClaudeStreamCtx()
	resp := &http.Response{Body: io.NopCloser(&failReader{err: errors.New("connection reset by peer")})}

	committed := false
	var counts usage.Counts
	res := streamSSE(c, resp, &counts, nil, "", func() { committed = true })

	if committed {
		t.Error("commit() must not run when the stream breaks before any byte (else retry is impossible)")
	}
	if res.wroteAny {
		t.Error("wroteAny must be false when nothing reached the client")
	}
	if res.sawTerminal {
		t.Error("sawTerminal must be false")
	}
	if res.err == nil {
		t.Error("err must be set so the caller knows to retry")
	}
	if w.Body.Len() != 0 {
		t.Errorf("nothing should be written to the client, got: %q", w.Body.String())
	}
}

// A stream ending in message_stop is reported complete; the terminal event and
// partial bytes reach the client and headers commit exactly once.
func TestStreamSSECompleted(t *testing.T) {
	body := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	c, w := newClaudeStreamCtx()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	commits := 0
	var counts usage.Counts
	res := streamSSE(c, resp, &counts, nil, "", func() { commits++ })

	if !res.sawTerminal {
		t.Error("stream ending in message_stop should report sawTerminal=true")
	}
	if res.err != nil {
		t.Errorf("a cleanly terminated stream must report err=nil, got: %v", res.err)
	}
	if commits != 1 {
		t.Errorf("headers must commit exactly once, got %d", commits)
	}
	if !strings.Contains(w.Body.String(), "message_stop") {
		t.Errorf("terminal event must reach the client, got: %q", w.Body.String())
	}
}

// A stream that delivers some bytes but EOFs without message_stop is reported
// truncated (so the caller logs it) while the partial bytes still pass through.
func TestStreamSSETruncatedAfterFirstByte(t *testing.T) {
	body := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\n"
	c, w := newClaudeStreamCtx()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	res := streamSSE(c, resp, &counts, nil, "", func() {})

	if res.sawTerminal {
		t.Error("a stream without message_stop must report sawTerminal=false")
	}
	if !res.wroteAny {
		t.Error("partial bytes were relayed, so wroteAny must be true")
	}
	if res.err == nil {
		t.Error("a truncated stream must report a non-nil err")
	}
	if !strings.Contains(w.Body.String(), "message_start") {
		t.Errorf("partial bytes must still reach the client, got: %q", w.Body.String())
	}
}
