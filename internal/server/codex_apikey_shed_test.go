package server

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/cc-core/usage"
)

// Upstream sheds load as an in-band error frame inside an otherwise-200 SSE
// stream. Forwarded verbatim, `server_is_overloaded` reaches the Codex CLI as
// ApiError::ServerOverloaded — TERMINAL for the session, surfacing to the user
// as "Our servers are currently overloaded". The same failure under nearly any
// other code lands in the CLI's Retryable arm and is merely backed off.
//
// The OAuth path has demoted these two codes since cc-core/codexerr landed; the
// API-key path (which now carries the bulk of Codex traffic) did not, so every
// capacity shed on a relay killed the user's session outright.
func runAPIKeySSE(t *testing.T, sse string) (*httptest.ResponseRecorder, usage.Counts, sseRelayOutcome) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	var counts usage.Counts
	out := streamSSEOpenAI(c, bufio.NewReader(strings.NewReader(sse)), &counts, "")
	return w, counts, out
}

func TestAPIKeyStreamDemotesServerOverloaded(t *testing.T) {
	sse := `data: {"type":"error","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded"}}` + "\n\n"
	w, _, out := runAPIKeySSE(t, sse)

	body := w.Body.String()
	if strings.Contains(body, "server_is_overloaded") {
		t.Errorf("the session-ending capacity code must not reach the client; got %q", body)
	}
	if !strings.Contains(body, "server_error") {
		t.Errorf("the code must be demoted to server_error so the CLI retries; got %q", body)
	}
	// The human-readable message must survive — the user still needs to know why.
	if !strings.Contains(body, "Our servers are currently overloaded") {
		t.Errorf("the upstream message must be preserved verbatim; got %q", body)
	}
	if !out.shed || !out.capacity {
		t.Errorf("the shed must be reported as capacity; got shed=%v capacity=%v", out.shed, out.capacity)
	}
}

// Quota and rate codes are account-scoped, not moment-scoped. The CLI handles
// them non-terminally already and parses its retry delay off the original code,
// so demoting them would destroy information for no gain.
func TestAPIKeyStreamDoesNotDemoteQuotaCodes(t *testing.T) {
	sse := `data: {"type":"error","error":{"code":"insufficient_quota","message":"out of credit"}}` + "\n\n"
	w, _, out := runAPIKeySSE(t, sse)

	if !strings.Contains(w.Body.String(), "insufficient_quota") {
		t.Errorf("a quota code must reach the client untouched; got %q", w.Body.String())
	}
	if !out.shed {
		t.Error("a quota frame is still a shed turn")
	}
	if out.capacity {
		t.Error("a quota code is not a capacity shed and must not be demoted")
	}
}

// A stream that stops without a terminal event is exactly what the client
// reports as "stream disconnected before completion". This path could not see
// it at all before, so the fault was invisible in the request log.
func TestAPIKeyStreamTracksTerminalEvent(t *testing.T) {
	cases := []struct {
		name string
		sse  string
		want bool
	}{
		{
			name: "responses terminal",
			sse:  `data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}` + "\n\n",
			want: true,
		},
		{
			name: "chat DONE sentinel",
			sse:  `data: {"id":"1","object":"chat.completion.chunk"}` + "\n\n" + "data: [DONE]\n\n",
			want: true,
		},
		{
			name: "truncated upstream",
			sse:  `data: {"type":"response.output_text.delta","delta":"hel"}` + "\n\n",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, out := runAPIKeySSE(t, tc.sse)
			if out.sawTerminal != tc.want {
				t.Errorf("sawTerminal = %v, want %v", out.sawTerminal, tc.want)
			}
		})
	}
}

// Billing must be read from the ORIGINAL payload: demotion rewrites the code,
// and a shed turn still has to account for whatever usage the upstream reported
// before it gave up.
func TestAPIKeyStreamBillsFromOriginalPayload(t *testing.T) {
	sse := `data: {"type":"response.completed","response":{"usage":{"input_tokens":42,"output_tokens":7}}}` + "\n\n"
	_, counts, _ := runAPIKeySSE(t, sse)
	if counts.InputTokens != 42 || counts.OutputTokens != 7 {
		t.Errorf("usage = in:%d out:%d, want in:42 out:7", counts.InputTokens, counts.OutputTokens)
	}
}

// The OAuth relay has demoted capacity codes since codexerr landed, but said
// nothing about it — and the demotion is invisible by design, because the CLI
// recovers. So neither the operator nor the request log ever recorded that
// upstream refused to serve; a capacity shed surfaced only as a turn that
// finished with no usage, which reads as a broken relay rather than a busy
// model. streamSSECodexBackend now reports the shed to its caller.
func TestOAuthStreamReportsCapacityShed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `data: {"type":"error","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded"}}` + "\n\n"
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	_, shed, _ := streamSSECodexBackend(c, resp, &counts)

	if !shed.shed || !shed.capacity {
		t.Fatalf("a server_is_overloaded frame must be reported as a capacity shed; got shed=%v capacity=%v", shed.shed, shed.capacity)
	}
	out := w.Body.String()
	if strings.Contains(out, "server_is_overloaded") {
		t.Errorf("the session-ending code must still be demoted on the way out; got %q", out)
	}
	if !strings.Contains(out, "Our servers are currently overloaded") {
		t.Errorf("the upstream message must survive demotion; got %q", out)
	}
}

// Quota is account-scoped, not capacity: reported as a shed, never demoted.
func TestOAuthStreamReportsQuotaShedWithoutDemoting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `data: {"type":"error","error":{"code":"insufficient_quota","message":"out of credit"}}` + "\n\n"
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	_, shed, _ := streamSSECodexBackend(c, resp, &counts)

	if !shed.shed {
		t.Error("a quota frame is a shed turn")
	}
	if shed.capacity {
		t.Error("quota is not a capacity shed and must not be demoted")
	}
	if !strings.Contains(w.Body.String(), "insufficient_quota") {
		t.Errorf("the quota code must reach the client untouched; got %q", w.Body.String())
	}
}
