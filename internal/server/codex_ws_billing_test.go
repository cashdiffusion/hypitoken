package server

import (
	"testing"

	"github.com/wjsoj/cc-core/usage"
)

// A WS session accumulates tokens across many turns on one socket. Billing must
// charge each turn only its own delta — never the running total — or a long
// session massively overcharges (the bug that made a 1h/170-turn session look
// like tens of millions of cache-read tokens). And the deltas must sum back to
// the session total, so nothing is lost either.
func TestCodexTurnDeltaBillsEachTurnOnce(t *testing.T) {
	turns := []usage.Counts{
		{InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 5000},
		{InputTokens: 1200, OutputTokens: 150, CacheReadTokens: 8000},
		{InputTokens: 900, OutputTokens: 300, CacheReadTokens: 12000},
	}

	var running usage.Counts
	var billed usage.Counts
	var summed usage.Counts

	for i, tr := range turns {
		running.Add(tr)
		delta := codexTurnDelta(running, billed)
		if delta.InputTokens != tr.InputTokens ||
			delta.OutputTokens != tr.OutputTokens ||
			delta.CacheReadTokens != tr.CacheReadTokens {
			t.Fatalf("turn %d billed %+v, want just this turn %+v (running total was %+v)", i, delta, tr, running)
		}
		if delta.Requests != 1 {
			t.Fatalf("turn %d: Requests=%d, want exactly 1 per turn", i, delta.Requests)
		}
		summed.Add(delta)
		billed = running
		billed.Requests = 0
	}

	if summed.InputTokens != running.InputTokens ||
		summed.OutputTokens != running.OutputTokens ||
		summed.CacheReadTokens != running.CacheReadTokens {
		t.Fatalf("summed deltas %+v != session total %+v", summed, running)
	}
	if summed.Requests != int64(len(turns)) {
		t.Fatalf("total billed requests=%d, want %d (one per turn)", summed.Requests, len(turns))
	}
}
