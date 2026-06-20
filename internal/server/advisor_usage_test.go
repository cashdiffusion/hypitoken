package server

import (
	"testing"

	"github.com/wjsoj/cc-core/advisor"
	"github.com/wjsoj/cc-core/usage"
)

// TestAdvisorIterationsParsing locks in the captured wire shape for the
// advisor server-side tool: a single /v1/messages response carries an
// `iterations` array where `type:"advisor_message"` entries are billed
// under their own `model` (e.g. claude-opus-4-7), separate from the
// orchestrator's top-level usage. Top-level usage is the sum of
// `type:"message"` iterations only — never advisor.
func TestAdvisorIterationsParsing(t *testing.T) {
	var c usage.Counts
	var sub advisor.SubUsage

	// Real shape captured from /v1/messages: 3 iterations, middle one is
	// the advisor opus call.
	payload := []byte(`{"type":"message_delta","usage":{
		"input_tokens":2,
		"output_tokens":503,
		"cache_creation_input_tokens":5566,
		"cache_read_input_tokens":80200,
		"iterations":[
			{"type":"message","input_tokens":1,"output_tokens":101,"cache_read_input_tokens":37916,"cache_creation_input_tokens":4368},
			{"type":"advisor_message","input_tokens":58464,"output_tokens":3083,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"model":"claude-opus-4-7"},
			{"type":"message","input_tokens":1,"output_tokens":402,"cache_read_input_tokens":42284,"cache_creation_input_tokens":1198}
		]
	}}`)
	mergeSSEUsage(&c, &sub, payload)

	// Top-level orchestrator counts unchanged — must NOT include advisor.
	if c.InputTokens != 2 || c.OutputTokens != 503 ||
		c.CacheCreateTokens != 5566 || c.CacheReadTokens != 80200 {
		t.Fatalf("top-level counts wrong: %+v", c)
	}
	snap := sub.Snapshot()
	adv, ok := snap["claude-opus-4-7"]
	if !ok {
		t.Fatalf("advisor counts missing; got %+v", snap)
	}
	if adv.InputTokens != 58464 || adv.OutputTokens != 3083 ||
		adv.CacheReadTokens != 0 || adv.CacheCreateTokens != 0 {
		t.Fatalf("advisor counts wrong: %+v", adv)
	}
	if len(snap) != 1 {
		t.Fatalf("expected exactly 1 advisor model, got %d", len(snap))
	}
}

// TestAdvisorIterationsCumulative confirms that observing iterations twice
// (e.g. message_start carries an empty/partial array followed by the full
// one in message_delta) overwrites rather than appends — Anthropic's SSE
// emits the full cumulative iterations slice on each event.
func TestAdvisorIterationsCumulative(t *testing.T) {
	var c usage.Counts
	var sub advisor.SubUsage

	mergeSSEUsage(&c, &sub, []byte(`{"type":"message_delta","usage":{
		"iterations":[{"type":"advisor_message","input_tokens":100,"output_tokens":50,"model":"claude-opus-4-7"}]
	}}`))
	mergeSSEUsage(&c, &sub, []byte(`{"type":"message_delta","usage":{
		"iterations":[{"type":"advisor_message","input_tokens":300,"output_tokens":150,"model":"claude-opus-4-7"}]
	}}`))

	got := sub.Snapshot()["claude-opus-4-7"]
	if got.InputTokens != 300 || got.OutputTokens != 150 {
		t.Fatalf("expected last-write-wins (300/150), got %+v", got)
	}
}

// TestAdvisorIterationsNonStreaming covers extractUsageFromJSON for the
// buffered (non-SSE) response path.
func TestAdvisorIterationsNonStreaming(t *testing.T) {
	var sub advisor.SubUsage
	body := []byte(`{"id":"msg_x","model":"claude-sonnet-4-6","usage":{
		"input_tokens":2,"output_tokens":503,
		"iterations":[
			{"type":"message","input_tokens":1,"output_tokens":101},
			{"type":"advisor_message","input_tokens":58464,"output_tokens":3083,"model":"claude-opus-4-7"},
			{"type":"message","input_tokens":1,"output_tokens":402}
		]
	}}`)
	c := extractUsageFromJSON(body, &sub)
	if c.InputTokens != 2 || c.OutputTokens != 503 {
		t.Fatalf("top-level: %+v", c)
	}
	got := sub.Snapshot()["claude-opus-4-7"]
	if got.InputTokens != 58464 || got.OutputTokens != 3083 {
		t.Fatalf("advisor: %+v", got)
	}
}

// TestAdvisorMissingModel: defensive — if Anthropic ever omits the model
// field on an advisor iteration, we route it to a sentinel rather than
// silently dropping the cost.
func TestAdvisorMissingModel(t *testing.T) {
	var c usage.Counts
	var sub advisor.SubUsage
	mergeSSEUsage(&c, &sub, []byte(`{"type":"message_delta","usage":{
		"iterations":[{"type":"advisor_message","input_tokens":10,"output_tokens":5}]
	}}`))
	if _, ok := sub.Snapshot()[advisor.FallbackModel]; !ok {
		t.Fatalf("expected sentinel bucket; got %+v", sub.Snapshot())
	}
}
