package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// The WS relay is a passthrough, which is exactly why the capacity frame needed
// special handling: forwarded verbatim, server_is_overloaded / slow_down are
// terminal for codex-rs and the user's session dies with "Selected model is at
// capacity. Please try a different model." Only the dispatch code may change —
// the message must survive so the user still sees why the turn failed.
func TestCodexWSClientFrameDemotesCapacityCodes(t *testing.T) {
	cases := []struct {
		name         string
		in           string
		wantShed     bool
		wantCapacity bool
		wantCode     string // "" means the frame must come back byte-identical
	}{
		{
			name:         "error event: overloaded is demoted",
			in:           `{"type":"error","error":{"code":"server_is_overloaded","message":"The selected model is at capacity."}}`,
			wantShed:     true,
			wantCapacity: true,
			wantCode:     "server_error",
		},
		{
			name:         "response.failed: slow_down is demoted",
			in:           `{"type":"response.failed","response":{"error":{"code":"slow_down","message":"Slow down."}}}`,
			wantShed:     true,
			wantCapacity: true,
			wantCode:     "server_error",
		},
		{
			name: "quota is a shed signal but keeps its own code",
			// The CLI has a non-terminal arm of its own for quota; demoting
			// would only hide why the turn failed. It still counts as a shed so
			// the session unsticks off this credential — that one IS account-scoped.
			in:           `{"type":"error","error":{"code":"insufficient_quota","message":"Out of credits."}}`,
			wantShed:     true,
			wantCapacity: false,
			wantCode:     "insufficient_quota",
		},
		{
			name:     "fatal frame is forwarded verbatim",
			in:       `{"type":"error","error":{"code":"invalid_prompt","message":"Blocked."}}`,
			wantShed: false,
		},
		{
			name:     "unrecognised code is fatal, forwarded verbatim",
			in:       `{"type":"error","error":{"code":"some_new_code","message":"?"}}`,
			wantShed: false,
		},
		{
			name:     "ordinary delta is untouched",
			in:       `{"type":"response.output_text.delta","delta":"hello"}`,
			wantShed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, shed, capacity := codexWSClientFrame([]byte(tc.in))
			if shed != tc.wantShed {
				t.Fatalf("shed = %t, want %t", shed, tc.wantShed)
			}
			// capacity is what decides whether the credential is blamed: a
			// capacity shed must leave the session's assignment alone.
			if capacity != tc.wantCapacity {
				t.Fatalf("capacity = %t, want %t", capacity, tc.wantCapacity)
			}
			if tc.wantCode == "" {
				if string(out) != tc.in {
					t.Fatalf("frame was rewritten:\n got %s\nwant %s", out, tc.in)
				}
				return
			}
			var got struct {
				Error *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
				Response *struct {
					Error *struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				} `json:"response"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("output is not valid JSON: %v (%s)", err, out)
			}
			e := got.Error
			if e == nil && got.Response != nil {
				e = got.Response.Error
			}
			if e == nil {
				t.Fatalf("error object vanished from the frame: %s", out)
			}
			if e.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", e.Code, tc.wantCode)
			}
			// The message is what the user reads; demotion must not touch it.
			if !strings.Contains(tc.in, e.Message) || e.Message == "" {
				t.Fatalf("message was altered or dropped: %q (frame %s)", e.Message, out)
			}
		})
	}
}
