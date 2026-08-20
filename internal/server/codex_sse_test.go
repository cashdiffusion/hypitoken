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
	if res := streamSSECodexBackend(c, resp, &counts, nil); res.sawTerminal {
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
	if res := streamSSECodexBackend(c, resp, &counts, nil); !res.sawTerminal {
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

// Capacity shed arrives as an in-band `error` frame followed by
// `response.failed`, inside an otherwise-200 stream. Forwarding the error frame
// would (a) commit the response, foreclosing failover, and (b) reach the CLI as
// ApiError::ServerOverloaded, terminal for the session. Withhold it all instead
// so the caller can retry on another credential invisibly.
//
// Production shows this is worth the trouble: ~16% of Codex turns shed, and the
// rate is account-scoped (one account served 135 turns with zero sheds while
// another shed 27% of 97 in the same window), so another credential really can
// serve the turn.
func TestStreamSSECodexBackendShedsCapacityBeforeOutput(t *testing.T) {
	for _, code := range []string{"server_is_overloaded", "slow_down", "rate_limit_exceeded"} {
		t.Run(code, func(t *testing.T) {
			c, w := newCodexStreamCtx()
			body := "event: error\n" +
				`data: {"type":"error","error":{"code":"` + code + `","message":"nope"}}` + "\n\n" +
				"event: response.failed\n" +
				`data: {"type":"response.failed","response":{"error":{"code":"` + code + `"}}}` + "\n\n"
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

			committed := false
			var counts usage.Counts
			res := streamSSECodexBackend(c, resp, &counts, func() { committed = true })

			if committed {
				t.Error("commit() must not run — the response must stay uncommitted so failover works")
			}
			if res.wroteAny {
				t.Error("wroteAny must be false so the caller's pre-output failover fires")
			}
			if res.sawTerminal {
				t.Error("sawTerminal must be false — the withheld response.failed must not look like a clean end")
			}
			if res.shed == "" {
				t.Error("shed must carry the withheld frame for diagnostics")
			}
			if got := w.Body.String(); got != "" {
				t.Errorf("nothing may reach the client, got %q", got)
			}
		})
	}
}

// Once output has started there is no failover left, so the frame must be
// forwarded — but demoted out of the CLI's session-ending code set so it
// retries instead of giving up. The human-readable message must survive.
func TestStreamSSECodexBackendDemotesCapacityAfterOutput(t *testing.T) {
	c, w := newCodexStreamCtx()
	body := "event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n" +
		"event: error\n" +
		`data: {"type":"error","error":{"code":"server_is_overloaded","message":"we are full"}}` + "\n\n"
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	res := streamSSECodexBackend(c, resp, &counts, func() {})

	if !res.wroteAny {
		t.Fatal("wroteAny must be true — output already started")
	}
	if res.shed != "" {
		t.Error("must not report a pre-output shed once output has started")
	}
	if !res.demoted.shed || !res.demoted.capacity {
		t.Errorf("the post-output shed must be reported for the log; got %+v", res.demoted)
	}
	out := w.Body.String()
	if strings.Contains(out, "server_is_overloaded") {
		t.Errorf("the session-ending code must be demoted, got %q", out)
	}
	if !strings.Contains(out, "server_error") {
		t.Errorf("expected the demoted code in the output, got %q", out)
	}
	if !strings.Contains(out, "we are full") {
		t.Errorf("the upstream message must survive verbatim, got %q", out)
	}
}

// An error that is the request's own fault must pass through untouched, before
// output or after: retrying it on another credential would fail identically and
// the client needs the real reason.
func TestStreamSSECodexBackendLeavesFatalErrorsAlone(t *testing.T) {
	frame := `{"type":"error","error":{"type":"invalid_request_error","code":"content_policy_violation","message":"blocked"}}`
	c, w := newCodexStreamCtx()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("event: error\ndata: " + frame + "\n\n"))}

	var counts usage.Counts
	res := streamSSECodexBackend(c, resp, &counts, func() {})

	if res.shed != "" {
		t.Errorf("a fatal error is not a shed; got %q", res.shed)
	}
	if out := w.Body.String(); !strings.Contains(out, frame) {
		t.Errorf("frame must be forwarded verbatim:\n want %q\n  got %q", frame, out)
	}
}
