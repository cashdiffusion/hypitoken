package kiroupstream

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wjsoj/cc-core/kirobridge"
)

func TestUnquotePartialJSON_Encoded(t *testing.T) {
	// Mimic what cc-core's StreamTranslator emits when Kiro upstream sends
	// `"input": "{\"file_path\":\"/foo\"}"` — partial_json carries the
	// JSON-string-encoded form (with surrounding quotes + escapes).
	encoded, _ := json.Marshal(`{"file_path":"/foo"}`)
	in := []byte(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":` + string(encoded) + `}}`)
	out, ok := unquotePartialJSON(in)
	if !ok {
		t.Fatal("expected patch to apply")
	}
	if !strings.Contains(string(out), `"partial_json":"{\"file_path\":\"/foo\"}"`) {
		t.Fatalf("unexpected output: %s", out)
	}
	// Reparse and confirm the partial_json value is the literal JSON
	// (not a re-encoded string).
	var v struct {
		Delta struct {
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if v.Delta.PartialJSON != `{"file_path":"/foo"}` {
		t.Fatalf("partial_json not unquoted: %q", v.Delta.PartialJSON)
	}
}

func TestUnquotePartialJSON_AlreadyLiteral(t *testing.T) {
	// When partial_json is already a JSON literal (object/value, not a
	// quoted string), we must NOT touch it.
	in := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":{"x":1}}}`)
	if _, ok := unquotePartialJSON(in); ok {
		t.Fatal("expected no patch")
	}
}

func TestUnquotePartialJSON_NonInputDelta(t *testing.T) {
	in := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)
	if _, ok := unquotePartialJSON(in); ok {
		t.Fatal("text_delta should not be touched")
	}
}

func TestRewriteToolName_Map(t *testing.T) {
	nm := kirobridge.ToolNameMap{}
	nm.Apply("short_abc", "this_is_a_really_long_tool_name_we_had_to_shorten")
	in := []byte(`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"u1","name":"short_abc","input":{}}}`)
	out, ok := rewriteToolName(in, nm)
	if !ok {
		t.Fatal("expected rewrite")
	}
	if !strings.Contains(string(out), `"name":"this_is_a_really_long_tool_name_we_had_to_shorten"`) {
		t.Fatalf("name not rewritten: %s", out)
	}
}
