package server

import (
	"net/http"
	"testing"
	"time"
)

func TestParseClaudeUsageLimitBody(t *testing.T) {
	now := time.Now()
	future := now.Add(3 * time.Hour).Truncate(time.Second).Unix()

	cases := []struct {
		name       string
		body       string
		wantOK     bool
		wantInPast bool // true => parsed t should be before now (i.e. fallback or no-op)
	}{
		{
			name:   "explicit timestamp",
			body:   `{"type":"error","error":{"type":"rate_limit_error","message":"Claude AI usage limit reached|` + itoa(future) + `"}}`,
			wantOK: true,
		},
		{
			name:   "marker without pipe → fallback 1h",
			body:   `{"error":{"message":"Claude AI usage limit reached"}}`,
			wantOK: true,
		},
		{
			name:   "marker with empty timestamp → fallback 1h",
			body:   `{"error":{"message":"Claude AI usage limit reached|"}}`,
			wantOK: true,
		},
		{
			name:   "case-insensitive marker",
			body:   `{"error":{"message":"CLAUDE AI USAGE LIMIT REACHED|` + itoa(future) + `"}}`,
			wantOK: true,
		},
		{
			name:   "stale timestamp falls back to 1h future",
			body:   `{"error":{"message":"Claude AI usage limit reached|1000000000"}}`,
			wantOK: true,
		},
		{
			name:   "no marker → not a usage limit",
			body:   `{"error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}`,
			wantOK: false,
		},
		{
			name:   "extra-usage rejection is not a usage limit",
			body:   `{"error":{"message":"Extra usage is required to process this request"}}`,
			wantOK: false,
		},
		{
			name:   "empty body",
			body:   ``,
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, ok := parseClaudeUsageLimitBody([]byte(tc.body))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && !ts.After(time.Now()) {
				t.Fatalf("parsed reset %v should be in the future", ts)
			}
		})
	}
}

func TestParseUnifiedRatelimitRejected(t *testing.T) {
	now := time.Now()
	in1h := now.Add(1 * time.Hour).Truncate(time.Second).Unix()
	in3h := now.Add(3 * time.Hour).Truncate(time.Second).Unix()
	in7d := now.Add(7 * 24 * time.Hour).Truncate(time.Second).Unix()
	stale := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Unix()

	tests := []struct {
		name       string
		headers    http.Header
		wantOK     bool
		wantBanned bool
		wantNear   time.Duration // |returned - expected| <= 5s; only checked when !wantBanned
		expected   time.Time
	}{
		{
			name: "all allowed → not rejected",
			headers: http.Header{
				"Anthropic-Ratelimit-Unified-Status":    []string{"allowed"},
				"Anthropic-Ratelimit-Unified-5h-Status": []string{"allowed"},
				"Anthropic-Ratelimit-Unified-7d-Status": []string{"allowed"},
			},
			wantOK: false,
		},
		{
			name: "top-level rejected with unified-reset → use unified-reset",
			headers: http.Header{
				"Anthropic-Ratelimit-Unified-Status": []string{"rejected"},
				"Anthropic-Ratelimit-Unified-Reset":  []string{itoa(in3h)},
			},
			wantOK:   true,
			wantNear: 5 * time.Second,
			expected: time.Unix(in3h, 0),
		},
		{
			name: "rejected_5h variant prefix matches",
			headers: http.Header{
				"Anthropic-Ratelimit-Unified-Status": []string{"rejected_5h"},
				"Anthropic-Ratelimit-Unified-Reset":  []string{itoa(in1h)},
			},
			wantOK:   true,
			wantNear: 5 * time.Second,
			expected: time.Unix(in1h, 0),
		},
		{
			name: "5h bucket rejected, no top-level → use 5h-reset",
			headers: http.Header{
				"Anthropic-Ratelimit-Unified-5h-Status": []string{"rejected"},
				"Anthropic-Ratelimit-Unified-5h-Reset":  []string{itoa(in1h)},
				"Anthropic-Ratelimit-Unified-7d-Status": []string{"allowed"},
			},
			wantOK:   true,
			wantNear: 5 * time.Second,
			expected: time.Unix(in1h, 0),
		},
		{
			name: "both buckets rejected → take later reset (7d)",
			headers: http.Header{
				"Anthropic-Ratelimit-Unified-5h-Status": []string{"rejected"},
				"Anthropic-Ratelimit-Unified-5h-Reset":  []string{itoa(in1h)},
				"Anthropic-Ratelimit-Unified-7d-Status": []string{"rejected"},
				"Anthropic-Ratelimit-Unified-7d-Reset":  []string{itoa(in7d)},
			},
			wantOK:   true,
			wantNear: 5 * time.Second,
			expected: time.Unix(in7d, 0),
		},
		{
			name: "top-level wins over bucket reset",
			headers: http.Header{
				"Anthropic-Ratelimit-Unified-Status":    []string{"rejected"},
				"Anthropic-Ratelimit-Unified-Reset":     []string{itoa(in1h)},
				"Anthropic-Ratelimit-Unified-5h-Status": []string{"rejected"},
				"Anthropic-Ratelimit-Unified-5h-Reset":  []string{itoa(in7d)},
			},
			wantOK:   true,
			wantNear: 5 * time.Second,
			expected: time.Unix(in1h, 0),
		},
		{
			name: "rejected with stale reset stamp → banned (no recovery time)",
			headers: http.Header{
				"Anthropic-Ratelimit-Unified-Status": []string{"rejected"},
				"Anthropic-Ratelimit-Unified-Reset":  []string{itoa(stale)},
			},
			wantOK:     true,
			wantBanned: true,
		},
		{
			name: "rejected with no reset header → banned (no recovery time)",
			headers: http.Header{
				"Anthropic-Ratelimit-Unified-Status": []string{"rejected"},
			},
			wantOK:     true,
			wantBanned: true,
		},
		{
			name: "bucket rejected but only past bucket-reset → banned",
			headers: http.Header{
				"Anthropic-Ratelimit-Unified-5h-Status": []string{"rejected"},
				"Anthropic-Ratelimit-Unified-5h-Reset":  []string{itoa(stale)},
			},
			wantOK:     true,
			wantBanned: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, banned, ok := parseUnifiedRatelimitRejected(tc.headers)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if banned != tc.wantBanned {
				t.Fatalf("banned = %v, want %v", banned, tc.wantBanned)
			}
			if !ok || banned {
				return
			}
			delta := got.Sub(tc.expected)
			if delta < 0 {
				delta = -delta
			}
			if delta > tc.wantNear {
				t.Fatalf("got %v, want within %v of %v (delta %v)", got, tc.wantNear, tc.expected, delta)
			}
		})
	}
}

// itoa avoids pulling strconv into the test just for one cheap conversion.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
