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

// codexSSE builds an upstream SSE body out of Responses event payloads.
func codexSSE(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	return b.String()
}

func TestStreamCodexAsChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	body := codexSSE(
		`{"type":"response.created","response":{"id":"resp_1"}}`,
		`{"type":"response.output_text.delta","delta":"hi"}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":11,"output_tokens":3,"input_tokens_details":{"cached_tokens":4}}}}`,
	)
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	res := streamCodexAsChatCompletions(c, resp.Body, &counts, "gpt-5.6-sol", false, func() {})
	if res.err != nil && !errors.Is(res.err, io.EOF) {
		t.Fatalf("relay error: %v", res.err)
	}
	if !res.sawTerminal {
		t.Error("terminal event not observed")
	}

	out := rec.Body.String()
	for _, want := range []string{
		`"object":"chat.completion.chunk"`,
		`"role":"assistant"`,
		`"content":"hi"`,
		`"finish_reason":"stop"`,
		"data: [DONE]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %s\n---\n%s", want, out)
		}
	}
	// No Responses event type may leak through to a chat client.
	if strings.Contains(out, "response.output_text.delta") {
		t.Errorf("raw Responses event leaked to the client:\n%s", out)
	}
	// Usage is read off the raw upstream events so billing matches the native
	// /v1/responses path exactly — including cc-core's convention of splitting
	// cached tokens out of input_tokens rather than double-counting them.
	if counts.InputTokens != 7 || counts.OutputTokens != 3 || counts.CacheReadTokens != 4 {
		t.Errorf("counts = %+v, want in=7 out=3 cache=4", counts)
	}
}

func TestStreamCodexAsChatCompletionsTruncatedUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	// Upstream dies mid-answer: no terminal event. The caller logs this as a
	// truncation, and the client must not have been told the stream ended
	// cleanly.
	body := codexSSE(`{"type":"response.output_text.delta","delta":"partial"}`)
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	res := streamCodexAsChatCompletions(c, resp.Body, &counts, "gpt-5.6-sol", false, func() {})
	if res.sawTerminal {
		t.Error("truncated upstream reported as terminal")
	}
	out := rec.Body.String()
	if !strings.Contains(out, `"content":"partial"`) {
		t.Errorf("partial content not delivered:\n%s", out)
	}
	if strings.Contains(out, "data: [DONE]") {
		t.Errorf("truncated stream must not be closed as clean:\n%s", out)
	}
}

func TestChatStreamWantsUsage(t *testing.T) {
	if !chatStreamWantsUsage([]byte(`{"stream_options":{"include_usage":true}}`)) {
		t.Error("include_usage:true not detected")
	}
	if chatStreamWantsUsage([]byte(`{"stream":true}`)) {
		t.Error("absent stream_options must not request usage")
	}
	if chatStreamWantsUsage([]byte(`not json`)) {
		t.Error("unparseable body must not request usage")
	}
}

func TestCodexModelUnsupported(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   bool
	}{
		{400, `{"error":{"code":"model_not_found"}}`, true},
		{404, `{"error":{"message":"The model gpt-4o-mini does not exist"}}`, true},
		{400, `{"error":{"message":"Unsupported model for this account"}}`, true},
		{503, `{"error":{"message":"No available channel for model gpt-5.2"}}`, false},
		// A real client error must still reach the client verbatim.
		{400, `{"error":{"message":"missing required parameter: messages"}}`, false},
		// Rate limits and 5xx are handled by their own branches upstream.
		{429, `{"error":{"code":"model_not_found"}}`, false},
		{500, `{"error":{"message":"unknown model"}}`, false},
	}
	for _, tc := range cases {
		if got := codexModelUnsupported(tc.status, []byte(tc.body)); got != tc.want {
			t.Errorf("codexModelUnsupported(%d, %s) = %v, want %v", tc.status, tc.body, got, tc.want)
		}
	}
}

// The chat bridge erases a shed. apicompat.Translate has no case for
// {"type":"error"}, so the frame yields nothing, and the response.failed that
// follows renders as an ordinary finish frame with finish_reason "stop". Before
// this was handled the client therefore received a well-formed, EMPTY,
// successful-looking completion — the model appearing to return nothing and
// stop normally — while sawTerminal=true suppressed the caller's failover and
// nothing was recorded anywhere. That is strictly worse than the native path,
// where the CLI at least sees the code and backs off.
func TestStreamCodexAsChatCompletionsWithholdsPreOutputShed(t *testing.T) {
	for _, code := range []string{"server_is_overloaded", "slow_down", "rate_limit_exceeded"} {
		t.Run(code, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			body := codexSSE(
				`{"type":"response.created","response":{"id":"resp_1"}}`,
				`{"type":"error","error":{"code":"`+code+`","message":"we are full"}}`,
				`{"type":"response.failed","response":{"status":"failed","error":{"code":"`+code+`"}}}`,
			)
			committed := false
			var counts usage.Counts
			res := streamCodexAsChatCompletions(c, strings.NewReader(body), &counts,
				"gpt-5.6-sol", false, func() { committed = true })

			if committed {
				t.Error("commit() must not run — the response must stay uncommitted so failover works")
			}
			if res.wroteAny {
				t.Error("wroteAny must be false so the caller's pre-output failover fires")
			}
			if res.sawTerminal {
				t.Error("the withheld response.failed must not be reported as a clean terminal")
			}
			if res.shed == "" {
				t.Error("the withheld frame must be reported so the caller retries elsewhere")
			}
			if got := rec.Body.String(); got != "" {
				t.Errorf("nothing may reach the client, got %q", got)
			}
		})
	}
}

// Once output has started there is no failover left. Demotion — the fallback
// the native relays use — buys nothing here either, because the code never
// reaches the client in the first place. So the only thing left is to record
// that it happened, which is what keeps a shed from reading as a healthy turn
// that merely stopped early.
func TestStreamCodexAsChatCompletionsRecordsPostOutputShed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	body := codexSSE(
		`{"type":"response.output_text.delta","delta":"partial"}`,
		`{"type":"error","error":{"code":"server_is_overloaded","message":"we are full"}}`,
		`{"type":"response.failed","response":{"status":"failed"}}`,
	)
	var counts usage.Counts
	res := streamCodexAsChatCompletions(c, strings.NewReader(body), &counts,
		"gpt-5.6-sol", false, func() {})

	if res.shed != "" {
		t.Errorf("must not report a withheld shed once output has started; got %q", res.shed)
	}
	if !res.demoted.shed || !res.demoted.capacity {
		t.Errorf("the post-output shed must be recorded; got %+v", res.demoted)
	}
	if !res.wroteAny {
		t.Error("wroteAny must be true — content already reached the client")
	}
	if out := rec.Body.String(); !strings.Contains(out, `"content":"partial"`) {
		t.Errorf("content delivered before the shed must survive:\n%s", out)
	}
}

// Quota and rate codes are account-scoped rather than capacity, and the caller
// labels the request log from that split.
func TestStreamCodexAsChatCompletionsSplitsQuotaFromCapacity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	body := codexSSE(
		`{"type":"response.output_text.delta","delta":"partial"}`,
		`{"type":"error","error":{"code":"insufficient_quota","message":"out of credit"}}`,
	)
	var counts usage.Counts
	res := streamCodexAsChatCompletions(c, strings.NewReader(body), &counts,
		"gpt-5.6-sol", false, func() {})

	if !res.demoted.shed {
		t.Error("a quota frame is still a shed")
	}
	if res.demoted.capacity {
		t.Error("quota is not a capacity shed")
	}
	if got := shedTurnLabel(res.demoted.capacity); got != "upstream shed the turn (quota/rate)" {
		t.Errorf("request-log label = %q", got)
	}
}

// A healthy turn must be untouched: openers produce no frames of their own, so
// the first byte is real content and commit fires exactly once with it.
func TestStreamCodexAsChatCompletionsCommitsOnFirstContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	body := codexSSE(
		`{"type":"response.created","response":{"id":"resp_1"}}`,
		`{"type":"response.in_progress","response":{"id":"resp_1"}}`,
		`{"type":"response.output_text.delta","delta":"hi"}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":11,"output_tokens":3}}}`,
	)
	commits := 0
	var counts usage.Counts
	res := streamCodexAsChatCompletions(c, strings.NewReader(body), &counts,
		"gpt-5.6-sol", false, func() { commits++ })

	if commits != 1 {
		t.Errorf("commit must fire exactly once, got %d", commits)
	}
	if !res.sawTerminal || !res.wroteAny {
		t.Errorf("healthy turn: sawTerminal=%v wroteAny=%v", res.sawTerminal, res.wroteAny)
	}
	if res.shed != "" || res.demoted.shed {
		t.Errorf("healthy turn must report no shed: %+v", res)
	}
	out := rec.Body.String()
	if !strings.Contains(out, `"content":"hi"`) || !strings.Contains(out, "data: [DONE]") {
		t.Errorf("healthy stream mangled:\n%s", out)
	}
	if counts.InputTokens != 11 || counts.OutputTokens != 3 {
		t.Errorf("usage lost: in=%d out=%d", counts.InputTokens, counts.OutputTokens)
	}
}
