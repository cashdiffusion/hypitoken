package server

import (
	"encoding/json"
	"testing"
	"time"
)

// A binding made in group A must never resolve in group B — the core cross-group
// 串号 safety boundary.
func TestCodexRespAccountStoreGroupIsolation(t *testing.T) {
	s := newCodexRespAccountStore(time.Hour)
	defer s.Close()

	s.Bind("groupA", "resp_123", "auth-1")

	if id, ok := s.Get("groupA", "resp_123"); !ok || id != "auth-1" {
		t.Errorf("same-group lookup = (%q,%v), want (auth-1,true)", id, ok)
	}
	if _, ok := s.Get("groupB", "resp_123"); ok {
		t.Error("a binding in groupA must NOT be visible in groupB")
	}
	if _, ok := s.Get("groupA", "resp_other"); ok {
		t.Error("unknown response id must miss")
	}
}

func TestCodexRespAccountStoreTTL(t *testing.T) {
	s := newCodexRespAccountStore(20 * time.Millisecond)
	defer s.Close()
	s.Bind("g", "resp_x", "auth-9")
	if _, ok := s.Get("g", "resp_x"); !ok {
		t.Fatal("binding should be present immediately")
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok := s.Get("g", "resp_x"); ok {
		t.Error("binding should have expired after TTL")
	}
}

func TestRemoveCodexPreviousResponseID(t *testing.T) {
	with := []byte(`{"model":"gpt-5","previous_response_id":"resp_abc","input":[{"a":1}]}`)
	out := removeCodexPreviousResponseID(with)
	if codexPreviousResponseID(out) != "" {
		t.Errorf("previous_response_id should be stripped, got body: %s", out)
	}
	// Other fields preserved.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("stripped body should still be valid JSON: %v", err)
	}
	if _, ok := obj["model"]; !ok {
		t.Error("model field must be preserved after stripping")
	}
	if _, ok := obj["input"]; !ok {
		t.Error("input field must be preserved after stripping")
	}

	// No-op when absent.
	without := []byte(`{"model":"gpt-5","input":[]}`)
	if got := removeCodexPreviousResponseID(without); string(got) != string(without) {
		t.Errorf("strip should be a no-op when field absent, got: %s", got)
	}
}

func TestCodexPreviousResponseID(t *testing.T) {
	if got := codexPreviousResponseID([]byte(`{"previous_response_id":"resp_z"}`)); got != "resp_z" {
		t.Errorf("extract = %q, want resp_z", got)
	}
	if got := codexPreviousResponseID([]byte(`{"model":"x"}`)); got != "" {
		t.Errorf("extract on absent = %q, want empty", got)
	}
	if got := codexPreviousResponseID([]byte(`not json`)); got != "" {
		t.Errorf("extract on invalid = %q, want empty", got)
	}
}
