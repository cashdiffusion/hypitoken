package server

import (
	"bufio"
	"net/http"
	"strings"
	"testing"

	"github.com/wjsoj/cc-core/usage"
)

// The novadiffusion / New-API relays stream the /v1/responses SSE back as
// `text/plain`, not `text/event-stream`. responseIsSSE must still recognise it
// via the body peek, otherwise the whole-body JSON parse loses usage (the
// gpt-5.5 "billing = $0" bug).
func TestResponseIsSSE(t *testing.T) {
	sse := "event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":304,\"output_tokens\":6}}}\n\n"

	cases := []struct {
		name string
		ct   string
		body string
		want bool
	}{
		{"event-stream header", "text/event-stream", sse, true},
		{"text/plain SSE (relay)", "text/plain; charset=utf-8", sse, true},
		{"text/plain SSE leading blank lines", "text/plain", "\n\n" + sse, true},
		{"plain json", "application/json", `{"id":"x","usage":{"input_tokens":1}}`, false},
		{"empty", "text/plain", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("Content-Type", tc.ct)
			br := bufio.NewReaderSize(strings.NewReader(tc.body), 64*1024)
			if got := responseIsSSE(h, br); got != tc.want {
				t.Fatalf("responseIsSSE = %v, want %v", got, tc.want)
			}
		})
	}
}

// peek must be non-consuming: after looksLikeSSE inspects the head, the full
// stream is still readable by streamSSEOpenAI.
func TestLooksLikeSSENonConsuming(t *testing.T) {
	body := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":304,\"output_tokens\":6}}}\n\n"
	br := bufio.NewReaderSize(strings.NewReader(body), 64*1024)
	if !looksLikeSSE(br) {
		t.Fatal("looksLikeSSE should be true for a data: line")
	}

	c, w := newCodexStreamCtx()
	var counts usage.Counts
	streamSSEOpenAI(c, br, &counts, "")
	if counts.InputTokens != 304 || counts.OutputTokens != 6 {
		t.Fatalf("usage lost after peek: in=%d out=%d", counts.InputTokens, counts.OutputTokens)
	}
	if !strings.Contains(w.Body.String(), "response.completed") {
		t.Fatalf("body not relayed verbatim, got: %q", w.Body.String())
	}
}

// End-to-end of the parse path: a text/plain SSE stream yields non-zero usage,
// while a single JSON body still parses via the non-stream branch.
func TestStreamSSEOpenAIParsesUsage(t *testing.T) {
	body := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":42,\"input_tokens_details\":{\"cached_tokens\":20}}}}\n\n" +
		"data: [DONE]\n\n"
	c, w := newCodexStreamCtx()
	br := bufio.NewReaderSize(strings.NewReader(body), 64*1024)
	var counts usage.Counts
	streamSSEOpenAI(c, br, &counts, "")

	// input - cached = 100 - 20 = 80; cached counted as CacheRead.
	if counts.InputTokens != 80 || counts.OutputTokens != 42 || counts.CacheReadTokens != 20 {
		t.Fatalf("usage mismatch: in=%d out=%d cacheR=%d", counts.InputTokens, counts.OutputTokens, counts.CacheReadTokens)
	}
	if counts.Requests != 1 {
		t.Fatalf("Requests = %d, want 1", counts.Requests)
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("stream not relayed verbatim, got: %q", w.Body.String())
	}
}

// Regression guard: a non-stream single JSON response still parses usage via
// extractOpenAIUsageFromJSON (the else branch), unchanged by the SSE fix.
func TestExtractOpenAIUsageSingleJSON(t *testing.T) {
	got := extractOpenAIUsageFromJSON([]byte(`{"id":"x","usage":{"prompt_tokens":12,"completion_tokens":8}}`))
	if got.InputTokens != 12 || got.OutputTokens != 8 || got.Requests != 1 {
		t.Fatalf("single-json usage mismatch: in=%d out=%d req=%d", got.InputTokens, got.OutputTokens, got.Requests)
	}
}
