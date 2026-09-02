package server

import (
	"testing"

	"github.com/wjsoj/cc-core/usage"
)

// TestMergeCodexUsageTakesTheLastTotalNotTheSum pins the contract that keeps a
// second usage snapshot from billing the prompt twice. Every OpenAI usage
// object is the total for its response; two of them in one stream describe one
// response, not two.
func TestMergeCodexUsageTakesTheLastTotalNotTheSum(t *testing.T) {
	var got usage.Counts
	// A running snapshot mid-stream, then the final one.
	mergeCodexUsage(&got, usage.Counts{InputTokens: 1200, OutputTokens: 40, CacheReadTokens: 800, Requests: 1})
	mergeCodexUsage(&got, usage.Counts{InputTokens: 1200, OutputTokens: 310, CacheReadTokens: 800, Requests: 1})

	want := usage.Counts{InputTokens: 1200, OutputTokens: 310, CacheReadTokens: 800, Requests: 1}
	if got != want {
		t.Fatalf("merged = %+v, want %+v — a second snapshot must overwrite, never add", got, want)
	}
}

// TestMergeCodexUsageIgnoresEmptyReports: the frames that carry no usage —
// almost all of them — must leave the totals alone, and Requests must stay
// latched once usage has been seen.
func TestMergeCodexUsageIgnoresEmptyReports(t *testing.T) {
	var got usage.Counts
	mergeCodexUsage(&got, usage.Counts{InputTokens: 50, OutputTokens: 5, Requests: 1})
	mergeCodexUsage(&got, usage.Counts{}) // a delta frame with usage: null
	if got.InputTokens != 50 || got.OutputTokens != 5 || got.Requests != 1 {
		t.Fatalf("an empty report changed the totals: %+v", got)
	}
	mergeCodexUsage(nil, usage.Counts{InputTokens: 1}) // must not panic
}

// TestCodexTurnDeltaAfterMergedTurns: with per-turn overlay feeding the session
// total, two turns of 100/10 must bill as two deltas of 100/10, and the session
// fold must read 200/20 — the invariant the WS pump relies on.
func TestCodexTurnDeltaAfterMergedTurns(t *testing.T) {
	var session, billed usage.Counts
	for i := 0; i < 2; i++ {
		var turn usage.Counts
		mergeCodexUsage(&turn, usage.Counts{InputTokens: 100, OutputTokens: 10, Requests: 1})
		mergeCodexUsage(&turn, usage.Counts{InputTokens: 100, OutputTokens: 10, Requests: 1}) // duplicate snapshot
		turn.Requests = 1
		session.Add(turn)
		d := codexTurnDelta(session, billed)
		if d.InputTokens != 100 || d.OutputTokens != 10 || d.Requests != 1 {
			t.Fatalf("turn %d delta = %+v, want 100/10/1", i+1, d)
		}
		billed = session
		billed.Requests = 0
	}
	if session.InputTokens != 200 || session.OutputTokens != 20 || session.Requests != 2 {
		t.Fatalf("session total = %+v, want 200/20/2", session)
	}
}
