package kiroupstream

import (
	"encoding/json"

	"github.com/wjsoj/cc-core/kirobridge"
)

// fixupSSEEvent post-processes a single SSE event emitted by cc-core's
// kirobridge.StreamTranslator before forwarding to the downstream client.
//
// It patches two bugs in cc-core v0.8.12's translator that surface as the
// classic Claude Code "Invalid tool parameters" error:
//
//  1. content_block_delta / input_json_delta: Kiro upstream sends the
//     toolUseEvent.input field as a JSON-encoded STRING (e.g. value is
//     "\"{\\\"file_path\\\":\\\"/foo\\\"}\""). cc-core stores it as
//     json.RawMessage which preserves the surrounding quotes + escapes,
//     then emits that verbatim as partial_json. Concatenating fragments
//     downstream yields a JSON STRING, not the OBJECT the client tool
//     schema expects. We unquote when the payload looks JSON-encoded.
//
//     kiro.rs avoids this by deserializing input as a plain Rust String
//     (serde auto-unquotes); cc-core's Go shape doesn't.
//
//  2. content_block_start / tool_use.name: cc-core shortens tool names
//     >63 chars to fit Kiro's hard limit and returns a (short→original)
//     map from Convert, but NewStreamTranslator doesn't accept the map,
//     so the SSE still carries the synthetic short name. The client
//     can't resolve it back to its own tool registry. Rewrite via the
//     nameMap that we kept from Convert.
//
// nameMap may be nil — only tools that hit the >63 char shortener are
// in it. Non-tool events flow through unchanged.
func fixupSSEEvent(ev kirobridge.SSEEvent, nameMap kirobridge.ToolNameMap) kirobridge.SSEEvent {
	switch ev.Name {
	case "content_block_delta":
		patched, ok := unquotePartialJSON(ev.Data)
		if ok {
			ev.Data = patched
		}
	case "content_block_start":
		if nameMap != nil {
			patched, ok := rewriteToolName(ev.Data, nameMap)
			if ok {
				ev.Data = patched
			}
		}
	}
	return ev
}

// unquotePartialJSON inspects a content_block_delta payload. If its
// delta.type is input_json_delta AND delta.partial_json is a JSON-encoded
// string, replace partial_json with the unquoted value. Returns (newData,
// true) when patched, (nil, false) otherwise — caller keeps original on
// false.
func unquotePartialJSON(data []byte) ([]byte, bool) {
	// Parse just enough to inspect delta.{type, partial_json}.
	var probe struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta struct {
			Type        string          `json:"type"`
			PartialJSON json.RawMessage `json:"partial_json"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, false
	}
	if probe.Delta.Type != "input_json_delta" {
		return nil, false
	}
	raw := probe.Delta.PartialJSON
	if len(raw) == 0 || raw[0] != '"' {
		// Already a literal JSON fragment (object/value), nothing to do.
		return nil, false
	}
	// raw is "\"...\"" — a JSON string. Unmarshal into a Go string,
	// then re-emit that string as the new partial_json value.
	var inner string
	if err := json.Unmarshal(raw, &inner); err != nil {
		return nil, false
	}
	out := map[string]any{
		"type":  "content_block_delta",
		"index": probe.Index,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": inner,
		},
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	return b, true
}

// rewriteToolName inspects a content_block_start payload. If its
// content_block.type is tool_use AND its name has a (short→original)
// mapping, rewrite name to the original.
func rewriteToolName(data []byte, nameMap kirobridge.ToolNameMap) ([]byte, bool) {
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, false
	}
	block, _ := probe["content_block"].(map[string]any)
	if block == nil {
		return nil, false
	}
	if t, _ := block["type"].(string); t != "tool_use" {
		return nil, false
	}
	short, _ := block["name"].(string)
	if short == "" {
		return nil, false
	}
	original := nameMap.Original(short)
	if original == short {
		return nil, false
	}
	block["name"] = original
	out, err := json.Marshal(probe)
	if err != nil {
		return nil, false
	}
	return out, true
}
