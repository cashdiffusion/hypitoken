package server

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
)

func TestClassifyUpstreamStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   upstreamFault
	}{
		{"ok", 200, faultNone},
		{"redirect is not a failure", 302, faultNone},

		{"unauthorized", 401, faultCredential},
		{"payment required", 402, faultCredential},
		{"forbidden", 403, faultCredential},

		{"request timeout is upstream weather", 408, faultUpstream},
		{"throttled", 429, faultUpstream},
		{"internal error", 500, faultUpstream},
		{"bad gateway", 502, faultUpstream},
		{"unavailable", 503, faultUpstream},
		{"gateway timeout", 504, faultUpstream},
		{"anthropic overloaded", 529, faultUpstream},

		// The whole point of the split: these are the caller's fault, so they
		// must not burn a retry on another credential or dent its health.
		{"malformed request", 400, faultClient},
		{"route not on this relay", 404, faultClient},
		{"method not allowed", 405, faultClient},
		{"payload too large", 413, faultClient},
		{"unprocessable", 422, faultClient},
		{"unknown 4xx defaults to client", 418, faultClient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyUpstreamStatus(tc.status); got != tc.want {
				t.Fatalf("classifyUpstreamStatus(%d) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestUpstreamFaultRetryable(t *testing.T) {
	if faultClient.retryable() {
		t.Fatal("a client-side fault must never be retried on another credential")
	}
	if faultNone.retryable() {
		t.Fatal("a success must never be retried")
	}
	if !faultUpstream.retryable() || !faultCredential.retryable() {
		t.Fatal("upstream and credential faults must both retry")
	}
}

func peekReader(s string) *bufio.Reader {
	return bufio.NewReaderSize(strings.NewReader(s), 64*1024)
}

func TestValidateAnthropicResponse(t *testing.T) {
	cases := []struct {
		name         string
		contentType  string
		body         string
		wantViolated bool
	}{
		{
			name:        "json object",
			contentType: "application/json",
			body:        `{"id":"msg_1","type":"message","usage":{"input_tokens":5}}`,
		},
		{
			name:        "sse stream",
			contentType: "text/event-stream",
			body:        "event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
		},
		{
			// Relays that stream SSE under the wrong Content-Type are a known
			// real-world quirk and must still pass — the check is shape-based.
			name:        "sse mislabelled as text/plain",
			contentType: "text/plain",
			body:        "data: {\"type\":\"content_block_delta\"}\n\n",
		},
		{
			name:        "json with leading whitespace",
			contentType: "",
			body:        "\n  {\"id\":\"msg_2\"}",
		},

		{
			// The incident this check exists for: a dead relay answering 200
			// with an HTML interstitial, previously billed as a zero-token
			// success and marked the credential healthy.
			name:         "html block page",
			contentType:  "text/html; charset=utf-8",
			body:         "<!DOCTYPE html><html><body>Access restricted</body></html>",
			wantViolated: true,
		},
		{
			name:         "plain text error",
			contentType:  "text/plain",
			body:         "upstream unavailable",
			wantViolated: true,
		},
		{
			name:         "empty body",
			contentType:  "application/json",
			body:         "",
			wantViolated: true,
		},
		{
			name:         "json array is not a messages response",
			contentType:  "application/json",
			body:         `[{"id":"msg_1"}]`,
			wantViolated: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.contentType != "" {
				h.Set("Content-Type", tc.contentType)
			}
			br := peekReader(tc.body)
			v := validateAnthropicResponse(h, br)
			if got := v.Detail != ""; got != tc.wantViolated {
				t.Fatalf("violated = %v (detail=%q), want %v", got, v.Detail, tc.wantViolated)
			}
			// The peek must not consume: the downstream relay/body read gets
			// the identical bytes.
			rest, _ := br.Peek(len(tc.body))
			if string(rest) != tc.body {
				t.Fatalf("validate consumed the body: got %q, want %q", rest, tc.body)
			}
		})
	}
}
