package server

import (
	"testing"

	"github.com/wjsoj/cc-core/usage"
)

// TestMergeSSEUsageNoDoubleCount locks in the fix for the streaming-billing
// bug where Add'ing usage from both message_start and message_delta double-
// counted input/cache tokens. message_delta in newer Anthropic API responses
// echoes the input/cache fields from message_start alongside the cumulative
// output_tokens; the two events must overlay (overwrite-if-positive), not
// sum.
func TestMergeSSEUsageNoDoubleCount(t *testing.T) {
	var c usage.Counts

	// message_start carries the input baseline + initial output estimate.
	mergeSSEUsage(&c, nil, []byte(`{"type":"message_start","message":{"usage":{"input_tokens":1000,"cache_read_input_tokens":500,"cache_creation_input_tokens":200,"output_tokens":1}}}`))
	if c.InputTokens != 1000 || c.CacheReadTokens != 500 || c.CacheCreateTokens != 200 || c.OutputTokens != 1 {
		t.Fatalf("after message_start: %+v", c)
	}

	// message_delta repeats input/cache and reports cumulative output.
	mergeSSEUsage(&c, nil, []byte(`{"type":"message_delta","usage":{"input_tokens":1000,"cache_read_input_tokens":500,"cache_creation_input_tokens":200,"output_tokens":250}}`))
	if c.InputTokens != 1000 {
		t.Fatalf("input_tokens doubled: got %d want 1000", c.InputTokens)
	}
	if c.CacheReadTokens != 500 {
		t.Fatalf("cache_read doubled: got %d want 500", c.CacheReadTokens)
	}
	if c.CacheCreateTokens != 200 {
		t.Fatalf("cache_create doubled: got %d want 200", c.CacheCreateTokens)
	}
	if c.OutputTokens != 250 {
		t.Fatalf("output_tokens: got %d want 250", c.OutputTokens)
	}
}

// TestMergeSSEUsageZerosDoNotClobber covers the case where message_delta
// only carries the final output_tokens and emits zero (or omits) input/
// cache fields — the baseline from message_start must survive.
func TestMergeSSEUsageZerosDoNotClobber(t *testing.T) {
	var c usage.Counts
	mergeSSEUsage(&c, nil, []byte(`{"type":"message_start","message":{"usage":{"input_tokens":42,"cache_read_input_tokens":7,"cache_creation_input_tokens":3,"output_tokens":1}}}`))
	mergeSSEUsage(&c, nil, []byte(`{"type":"message_delta","usage":{"output_tokens":99}}`))

	if c.InputTokens != 42 {
		t.Fatalf("input_tokens clobbered: got %d", c.InputTokens)
	}
	if c.CacheReadTokens != 7 {
		t.Fatalf("cache_read clobbered: got %d", c.CacheReadTokens)
	}
	if c.CacheCreateTokens != 3 {
		t.Fatalf("cache_create clobbered: got %d", c.CacheCreateTokens)
	}
	if c.OutputTokens != 99 {
		t.Fatalf("output_tokens: got %d want 99", c.OutputTokens)
	}
}

// TestMergeSSEUsageMalformed: bad JSON / missing usage shouldn't panic or
// modify the destination.
func TestMergeSSEUsageMalformed(t *testing.T) {
	c := usage.Counts{InputTokens: 5, OutputTokens: 9}
	mergeSSEUsage(&c, nil, []byte(`not-json`))
	mergeSSEUsage(&c, nil, []byte(`{"type":"ping"}`))
	mergeSSEUsage(&c, nil, []byte(`{"type":"message_delta"}`))
	if c.InputTokens != 5 || c.OutputTokens != 9 {
		t.Fatalf("counts mutated by malformed payloads: %+v", c)
	}
}
