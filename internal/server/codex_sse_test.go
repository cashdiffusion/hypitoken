package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/cc-core/usage"
)

func TestCodexTerminalEvent(t *testing.T) {
	terminal := []string{
		`{"type":"response.completed","response":{"usage":{}}}`,
		`{"type":"response.failed"}`,
		`{"type":"response.incomplete"}`,
		`{"type":"response.cancelled"}`,
		`{"type":"response.canceled"}`,
	}
	for _, p := range terminal {
		if !codexTerminalEvent([]byte(p)) {
			t.Errorf("expected terminal event for %s", p)
		}
	}
	nonTerminal := []string{
		`{"type":"response.output_item.done"}`,
		`{"type":"response.output_text.delta","delta":"hi"}`,
		`{"type":"response.created"}`,
		`not json`,
		``,
	}
	for _, p := range nonTerminal {
		if codexTerminalEvent([]byte(p)) {
			t.Errorf("did not expect terminal event for %q", p)
		}
	}
}

func newCodexStreamCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c, w
}

// A stream that EOFs without a terminal event is reported as truncated, but the
// bytes already received are still passed through to the client verbatim.
func TestStreamSSECodexBackendTruncated(t *testing.T) {
	body := "data: {\"type\":\"response.created\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
	c, w := newCodexStreamCtx()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	if sawTerminal, _, _ := streamSSECodexBackend(c, resp, &counts); sawTerminal {
		t.Error("stream without a terminal event should report sawTerminal=false")
	}
	if !strings.Contains(w.Body.String(), "response.output_text.delta") {
		t.Errorf("partial bytes must still reach the client, got: %q", w.Body.String())
	}
}

// A stream ending with response.completed is reported complete and forwarded
// verbatim.
func TestStreamSSECodexBackendCompleted(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}}\n\n"
	c, w := newCodexStreamCtx()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	if sawTerminal, _, _ := streamSSECodexBackend(c, resp, &counts); !sawTerminal {
		t.Error("stream ending in response.completed should report sawTerminal=true")
	}
	if !strings.Contains(w.Body.String(), "response.completed") {
		t.Errorf("terminal event must reach the client, got: %q", w.Body.String())
	}
	if counts.OutputTokens != 5 || counts.InputTokens != 10 {
		t.Errorf("usage must be extracted from response.completed, got in=%d out=%d", counts.InputTokens, counts.OutputTokens)
	}
}

// TestClientCancelIsNotUpstreamTruncation pins the distinction that a
// production log audit showed was being lost: a stream ending without a
// terminal event has two very different causes, and only one of them is a
// fault.
//
// Codex CLI aborts the in-flight request when the user hits Ctrl-C / ESC.
// That cancels the request context and surfaces to the relay as a read error
// — byte-for-byte the same signal as an upstream that hung up. Before the
// context was consulted, every such cancellation was recorded as "stream
// truncated before terminal event": ~90% of all Codex errors in production
// were ordinary user behaviour wearing an upstream-incident label, which
// buried the ~0.05% of real h2 truncations underneath them.
func TestClientCancelIsNotUpstreamTruncation(t *testing.T) {
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name           string
		ctx            context.Context
		err            error
		wantDisconnect bool
	}{
		{
			name:           "client hung up mid-stream",
			ctx:            cancelledCtx,
			err:            context.Canceled,
			wantDisconnect: true,
		},
		{
			// The upstream really did go away: the context is still live, so
			// this stays an upstream truncation and keeps its warning.
			name:           "upstream h2 truncation",
			ctx:            context.Background(),
			err:            errors.New("stream error: stream ID 5; INTERNAL_ERROR; received from peer"),
			wantDisconnect: false,
		},
		{
			// A bare context.Canceled with a live context means the transport
			// aborted on its own — an upstream fault, not the client leaving.
			name:           "transport-level cancel with live context",
			ctx:            context.Background(),
			err:            context.Canceled,
			wantDisconnect: false,
		},
		{
			name:           "clean end of stream",
			ctx:            cancelledCtx,
			err:            nil,
			wantDisconnect: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isClientDisconnect(tc.ctx, tc.err); got != tc.wantDisconnect {
				t.Fatalf("isClientDisconnect = %v, want %v", got, tc.wantDisconnect)
			}
		})
	}
}

// Upstream sheds capacity as an in-band error frame inside an otherwise-200
// stream. Forwarded verbatim it reaches the CLI as ApiError::ServerOverloaded,
// which ends the session ("Selected model is at capacity"); under nearly any
// other code the CLI just backs off and retries. This path commits headers
// eagerly so it cannot withhold and fail over — demotion is the fix available
// here.
func TestStreamSSECodexBackendDemotesCapacityCodes(t *testing.T) {
	for _, code := range []string{"server_is_overloaded", "slow_down"} {
		t.Run(code, func(t *testing.T) {
			c, w := newCodexStreamCtx()
			body := "event: error\n" +
				`data: {"type":"error","error":{"code":"` + code + `","message":"we are full"}}` + "\n\n"
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

			var counts usage.Counts
			_, _, _ = streamSSECodexBackend(c, resp, &counts)

			out := w.Body.String()
			if strings.Contains(out, code) {
				t.Errorf("session-ending code %q must be demoted, got %q", code, out)
			}
			if !strings.Contains(out, "server_error") {
				t.Errorf("expected the demoted code, got %q", out)
			}
			if !strings.Contains(out, "we are full") {
				t.Errorf("the upstream message must survive verbatim, got %q", out)
			}
		})
	}
}

// Codes the CLI already retries, and errors that are the request's own fault,
// must both pass through untouched — the former needs no help, the latter must
// keep its real reason.
func TestStreamSSECodexBackendLeavesOtherErrorsAlone(t *testing.T) {
	for _, frame := range []string{
		`{"type":"error","error":{"code":"rate_limit_exceeded","message":"limited"}}`,
		`{"type":"error","error":{"type":"invalid_request_error","code":"content_policy_violation","message":"blocked"}}`,
	} {
		c, w := newCodexStreamCtx()
		resp := &http.Response{Body: io.NopCloser(strings.NewReader("event: error\ndata: " + frame + "\n\n"))}

		var counts usage.Counts
		_, _, _ = streamSSECodexBackend(c, resp, &counts)

		if out := w.Body.String(); !strings.Contains(out, frame) {
			t.Errorf("frame must be forwarded verbatim:\n want %q\n  got %q", frame, out)
		}
	}
}
