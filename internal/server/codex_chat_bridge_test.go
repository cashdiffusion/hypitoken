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
	sawTerminal, err := streamCodexAsChatCompletions(c, resp, &counts, "gpt-5.6-sol", false)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("relay error: %v", err)
	}
	if !sawTerminal {
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
	sawTerminal, _ := streamCodexAsChatCompletions(c, resp, &counts, "gpt-5.6-sol", false)
	if sawTerminal {
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
