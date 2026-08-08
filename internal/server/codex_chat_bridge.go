package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/cc-core/apicompat"
	ccstream "github.com/wjsoj/cc-core/stream"
	"github.com/wjsoj/cc-core/usage"
)

// codex_chat_bridge.go is the transport half of the /v1/chat/completions bridge
// for Codex OAuth credentials. The protocol translation itself lives in
// cc-core's apicompat package — CPA-Claude has the same gap and cc-core already
// owns Codex request shaping (mimicry.SanitizeCodexRequestBody), so the mapping
// belongs there and only the gin/SSE plumbing stays here.

// streamCodexAsChatCompletions relays the backend's Responses SSE stream to the
// client as a chat.completion.chunk stream. Keepalive, write serialization and
// terminal tracking come from the same cc-core relay the native /v1/responses
// passthrough uses, so a bridged stream behaves identically under a slow
// upstream or an intermediary idle timeout.
func streamCodexAsChatCompletions(c *gin.Context, resp *http.Response, counts *usage.Counts, model string, includeUsage bool) (sawTerminal bool, streamErr error) {
	flusher, _ := c.Writer.(http.Flusher)
	reader := newLineReader(resp.Body)
	st := apicompat.NewStreamState(model, includeUsage, time.Now().Unix())

	// One upstream event can expand into several chat frames (role + tool-call
	// header + delta) and most expand into none, so pending holds the overflow
	// and Next keeps its one-frame-per-call contract.
	var pending [][]byte
	next := func() (out []byte, terminal bool, err error) {
		for {
			if len(pending) > 0 {
				out, pending = pending[0], pending[1:]
				return out, apicompat.IsDoneFrame(out), nil
			}
			line, rerr := reader.readLine()
			if len(line) > 0 {
				trim := bytes.TrimRight(line, "\r\n")
				if bytes.HasPrefix(trim, []byte("data:")) {
					payload := bytes.TrimSpace(trim[5:])
					if len(payload) > 0 && payload[0] == '{' {
						// Usage is read off the raw upstream event, not the
						// translated frames, so billing stays identical to the
						// native /v1/responses path.
						counts.Add(extractCodexBackendUsageFromJSON(payload))
						frames, _ := st.Translate(payload)
						pending = append(pending, frames...)
					}
				}
			}
			if rerr != nil {
				if len(pending) > 0 {
					continue
				}
				return nil, false, rerr
			}
		}
	}

	r := ccstream.Relay(c.Writer, func() {
		if flusher != nil {
			flusher.Flush()
		}
	}, ccstream.RelayOptions{
		KeepaliveIdle:    10 * time.Second,
		KeepalivePayload: []byte(":\n\n"),
		Next:             next,
	})
	return r.SawTerminal, r.Err
}

// chatStreamWantsUsage reports whether the client asked for the trailing
// usage-bearing chunk (stream_options.include_usage). Emitting it
// unconditionally breaks clients that assume every chunk carries a choice.
func chatStreamWantsUsage(body []byte) bool {
	var raw struct {
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	return json.Unmarshal(body, &raw) == nil && raw.StreamOptions.IncludeUsage
}

// codexModelUnsupported reports whether an upstream 4xx is the backend saying
// it doesn't host this model, as opposed to a genuine client error.
//
// This is what keeps the bridge from turning a working request into a hard
// failure: relay-only model names (gpt-5.6-terra-high, gpt-4o-mini, …) now
// reach an OAuth credential first, and a model rejection must roll back to the
// API-key pool that does host them instead of surfacing a 400 the client can do
// nothing about.
func codexModelUnsupported(status int, body []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusNotFound {
		return false
	}
	lower := bytes.ToLower(body)
	for _, needle := range []string{
		"model_not_found", "does not exist", "unsupported model",
		"unknown model", "invalid model", "not supported", "no available channel",
	} {
		if bytes.Contains(lower, []byte(needle)) {
			return true
		}
	}
	return false
}
