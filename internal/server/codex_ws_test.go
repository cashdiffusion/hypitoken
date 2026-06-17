package server

import (
	"net/http"
	"testing"
)

func TestIsCodexWSUpgrade(t *testing.T) {
	yes := &http.Request{Header: http.Header{}}
	yes.Header.Set("Upgrade", "websocket")
	yes.Header.Set("Connection", "Upgrade")
	if !isCodexWSUpgrade(yes) {
		t.Error("a websocket upgrade request should be detected")
	}
	// Case-insensitive + combined Connection header.
	yes2 := &http.Request{Header: http.Header{}}
	yes2.Header.Set("Upgrade", "WebSocket")
	yes2.Header.Set("Connection", "keep-alive, Upgrade")
	if !isCodexWSUpgrade(yes2) {
		t.Error("case-insensitive upgrade with combined Connection should be detected")
	}
	no := &http.Request{Header: http.Header{}}
	no.Header.Set("Connection", "keep-alive")
	if isCodexWSUpgrade(no) {
		t.Error("a plain request must not be detected as an upgrade")
	}
}

func TestCodexWSUpstreamURL(t *testing.T) {
	cases := map[string]string{
		"https://chatgpt.com/backend-api":   "wss://chatgpt.com/backend-api/codex/responses",
		"https://chatgpt.com/backend-api/":  "wss://chatgpt.com/backend-api/codex/responses",
		"http://localhost:8080/backend-api": "ws://localhost:8080/backend-api/codex/responses",
	}
	for in, want := range cases {
		if got := codexWSUpstreamURL(in); got != want {
			t.Errorf("codexWSUpstreamURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCodexWSExtractModel(t *testing.T) {
	if got := codexWSExtractModel([]byte(`{"model":"gpt-5-codex"}`)); got != "gpt-5-codex" {
		t.Errorf("top-level model = %q", got)
	}
	if got := codexWSExtractModel([]byte(`{"response":{"model":"gpt-5"}}`)); got != "gpt-5" {
		t.Errorf("nested model = %q", got)
	}
	if got := codexWSExtractModel([]byte(`{"foo":1}`)); got != "" {
		t.Errorf("absent model = %q, want empty", got)
	}
}

func TestCodexResponseID(t *testing.T) {
	if got := codexResponseID([]byte(`{"type":"response.completed","response":{"id":"resp_abc"}}`)); got != "resp_abc" {
		t.Errorf("response id = %q, want resp_abc", got)
	}
	if got := codexResponseID([]byte(`{"type":"response.output_text.delta"}`)); got != "" {
		t.Errorf("no response id = %q, want empty", got)
	}
}
