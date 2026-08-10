package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/usage"
)

// This file pins the two billing invariants that a 2026-08 audit of 825,929
// production request-log rows showed were being violated in opposite
// directions. Both reduce to one rule: only an OBSERVED quantity may be billed.
//
//	Anthropic paths — counts.Requests was hard-set to 1 before the response was
//	  read, so the `counts.Requests > 0` billing gate was a tautology. 3,808
//	  successful responses that reported no usage priced out to $0 and were
//	  served free; 3,805 belonged to paying accounts, concentrated in the
//	  costliest models (opus-4-8 ×2013, fable-5 ×1046, opus-4-7 ×335).
//
//	Codex API-key path — a response with no usage was charged an estimate
//	  derived from the request body size (input ≈ len(body)/4, output a flat
//	  1000). Across 233 charged rows that produced $170.28, the largest single
//	  row claiming 2,258,861 input tokens for $11.32. It also could not tell a
//	  broken relay from a user pressing Ctrl-C, so every cancellation was both
//	  overcharged and counted as a credential fault.

// ─── Anthropic: Requests must reflect observation, not completion ────────────

func TestAnthropicUsageRequestsOnlyOnObservation(t *testing.T) {
	t.Run("non-stream response without usage is unbillable", func(t *testing.T) {
		// A 200 whose body carries no usage object at all — the exact shape
		// behind the 3,808 free rows.
		got := extractUsageFromJSON([]byte(`{"id":"msg_1","type":"message","content":[]}`), nil)
		if got.Requests != 0 {
			t.Fatalf("Requests=%d want 0 — a response with no usage must not pass "+
				"the billing gate", got.Requests)
		}
		if !usage.MissingUsage(got) {
			t.Fatal("MissingUsage must report true so the caller can log and skip the charge")
		}
	})

	t.Run("non-stream response with usage is billable", func(t *testing.T) {
		got := extractUsageFromJSON([]byte(
			`{"usage":{"input_tokens":10,"output_tokens":4,"cache_read_input_tokens":900}}`), nil)
		if got.Requests != 1 {
			t.Fatalf("Requests=%d want 1", got.Requests)
		}
		if got.InputTokens != 10 || got.OutputTokens != 4 || got.CacheReadTokens != 900 {
			t.Fatalf("counts=%+v", got)
		}
	})

	t.Run("a stream that reports usage once stays billable", func(t *testing.T) {
		var c usage.Counts
		mergeSSEUsage(&c, nil, []byte(`{"type":"message_start","message":{"usage":{"input_tokens":50}}}`))
		if c.Requests != 1 {
			t.Fatalf("Requests=%d want 1 after message_start carried usage", c.Requests)
		}
		// A later event that omits every field must not un-observe the stream.
		mergeSSEUsage(&c, nil, []byte(`{"type":"message_delta","usage":{}}`))
		if c.Requests != 1 {
			t.Fatalf("Requests=%d want 1 — observation latches, it does not toggle", c.Requests)
		}
	})

	t.Run("a stream that never reports usage is unbillable", func(t *testing.T) {
		var c usage.Counts
		mergeSSEUsage(&c, nil, []byte(`{"type":"message_start","message":{"id":"x"}}`))
		mergeSSEUsage(&c, nil, []byte(`{"type":"content_block_delta","delta":{"text":"hi"}}`))
		if c.Requests != 0 {
			t.Fatalf("Requests=%d want 0", c.Requests)
		}
		if !usage.MissingUsage(c) {
			t.Fatal("a stream with no usage event must read as missing usage")
		}
	})
}

// ─── Anthropic: 5m/1h cache-write breakdown ─────────────────────────────────

// The breakdown is recorded whenever the upstream sends it, but recording it
// must not move any bill on its own — the built-in price cards leave the 1h
// rate zero, so the split is inert until an operator opts in.
func TestCacheCreationBreakdownIsObservationOnly(t *testing.T) {
	body := []byte(`{"usage":{"input_tokens":10,"output_tokens":5,` +
		`"cache_creation_input_tokens":1000,` +
		`"cache_creation":{"ephemeral_5m_input_tokens":250,"ephemeral_1h_input_tokens":750}}}`)
	got := extractUsageFromJSON(body, nil)

	if got.CacheCreateTokens != 1000 {
		t.Fatalf("CacheCreateTokens=%d want 1000 — the total must stay the full "+
			"cache-write count, never net of the 1h subset", got.CacheCreateTokens)
	}
	if got.CacheCreate1hTokens != 750 {
		t.Fatalf("CacheCreate1hTokens=%d want 750", got.CacheCreate1hTokens)
	}
	if got.CacheCreate5mTokens() != 250 {
		t.Fatalf("CacheCreate5mTokens()=%d want 250", got.CacheCreate5mTokens())
	}

	// Same request under a card with no 1h rate → identical charge to before.
	withBreakdown := got
	flat := got
	flat.CacheCreate1hTokens = 0
	cat := pricing.NewCatalog(pricing.Config{})
	a := cat.Cost("anthropic", "claude-sonnet-4-6", withBreakdown)
	b := cat.Cost("anthropic", "claude-sonnet-4-6", flat)
	if a != b {
		t.Fatalf("recording the breakdown changed the bill: %v != %v — enabling the "+
			"1h rate must remain an explicit operator decision", a, b)
	}
}

// An upstream that sends no breakdown must leave the subset at zero rather than
// guessing, so pricing degrades to the single-rate path instead of mispricing.
func TestCacheCreationBreakdownAbsent(t *testing.T) {
	got := extractUsageFromJSON([]byte(`{"usage":{"cache_creation_input_tokens":1000}}`), nil)
	if got.CacheCreate1hTokens != 0 {
		t.Fatalf("CacheCreate1hTokens=%d want 0 when the upstream reports no breakdown",
			got.CacheCreate1hTokens)
	}
	if got.CacheCreate5mTokens() != 1000 {
		t.Fatalf("CacheCreate5mTokens()=%d want 1000", got.CacheCreate5mTokens())
	}
}

// The breakdown also has to survive the streaming path's overwrite-if-positive
// merge, which is where the cache fields actually arrive in production.
func TestCacheCreationBreakdownOverStream(t *testing.T) {
	var c usage.Counts
	mergeSSEUsage(&c, nil, []byte(`{"type":"message_start","message":{"usage":{`+
		`"input_tokens":8,"cache_creation_input_tokens":2000,`+
		`"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":2000}}}}`))
	mergeSSEUsage(&c, nil, []byte(`{"type":"message_delta","usage":{"output_tokens":33}}`))

	if c.CacheCreate1hTokens != 2000 {
		t.Fatalf("CacheCreate1hTokens=%d want 2000 (must survive the delta merge)", c.CacheCreate1hTokens)
	}
	if c.CacheCreateTokens != 2000 || c.OutputTokens != 33 {
		t.Fatalf("counts=%+v", c)
	}
}

// ─── Codex API-key: no estimate, and cancel ≠ fault ─────────────────────────

func TestCodexStreamOutcomeClassification(t *testing.T) {
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":" +
		"{\"input_tokens\":120,\"output_tokens\":40,\"input_tokens_details\":{\"cached_tokens\":100}}}}\n\n"

	t.Run("completed stream bills observed usage", func(t *testing.T) {
		c, counts, gone := runCodexStream(t, sse, false)
		_ = c
		if gone {
			t.Fatal("clientGone must be false for a stream the client read to the end")
		}
		o := usage.ClassifyStreamOutcome(counts, gone)
		if o != usage.StreamComplete || !o.Billable(counts) {
			t.Fatalf("outcome=%v billable=%v", o, o.Billable(counts))
		}
		// cached tokens split off the full-rate input axis
		if counts.InputTokens != 20 || counts.CacheReadTokens != 100 || counts.OutputTokens != 40 {
			t.Fatalf("counts=%+v", counts)
		}
	})

	t.Run("upstream without usage bills nothing and faults the credential", func(t *testing.T) {
		// A relay that streams content but drops the terminal usage chunk — the
		// shape behind all 233 estimated rows.
		noUsage := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
		_, counts, gone := runCodexStream(t, noUsage, false)
		o := usage.ClassifyStreamOutcome(counts, gone)
		if o != usage.StreamUpstreamNoUsage {
			t.Fatalf("outcome=%v want StreamUpstreamNoUsage", o)
		}
		if o.Billable(counts) {
			t.Fatal("BILLING REGRESSION: an unreported stream must cost $0. The removed " +
				"estimator charged $170.28 across 233 such rows, one of them $11.32.")
		}
		if !o.CredentialFault() {
			t.Error("a relay that cannot account for what it served must be cooled")
		}
		if o.LogError() != usage.MissingUsageError {
			t.Errorf("LogError=%q want %q", o.LogError(), usage.MissingUsageError)
		}
	})

	t.Run("client cancel is health-neutral and never estimated", func(t *testing.T) {
		_, counts, _ := runCodexStream(t, sse, true)
		o := usage.ClassifyStreamOutcome(counts, true)
		if o != usage.StreamClientCanceled {
			t.Fatalf("outcome=%v want StreamClientCanceled", o)
		}
		if o.CredentialFault() {
			t.Fatal("HEALTH REGRESSION: a client hang-up must not fault the credential — " +
				"this turned every Ctrl-C into a breaker trip on a healthy key")
		}
		if o.LogError() != usage.ClientCanceledError {
			t.Errorf("LogError=%q want %q", o.LogError(), usage.ClientCanceledError)
		}
	})
}

// A canceled request context must be reported as a client hang-up, not as an
// upstream fault — they are indistinguishable at the reader, so the context is
// the only honest discriminator.
func TestCodexStreamCanceledContextReportsClientGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	cancel()

	var counts usage.Counts
	// Reader hits EOF immediately; with the context already canceled that must
	// classify as the client leaving.
	gone := streamSSEOpenAI(c, bufio.NewReader(strings.NewReader("")), &counts, "")
	if !gone {
		t.Fatal("a read error under a canceled request context is a client hang-up")
	}
	if o := usage.ClassifyStreamOutcome(counts, gone); o.CredentialFault() {
		t.Fatal("client cancellation must not be charged against credential health")
	}
}

// runCodexStream drives streamSSEOpenAI over a canned SSE body. When
// cancelClient is set the request context is canceled before the read, standing
// in for a client that hung up.
func runCodexStream(t *testing.T, sse string, cancelClient bool) (*gin.Context, usage.Counts, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	if cancelClient {
		cancel()
	} else {
		defer cancel()
	}
	var counts usage.Counts
	gone := streamSSEOpenAI(c, bufio.NewReader(strings.NewReader(sse)), &counts, "")
	return c, counts, gone
}
