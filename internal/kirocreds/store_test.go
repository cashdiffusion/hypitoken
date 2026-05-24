package kirocreds

import (
	"path/filepath"
	"testing"

	"github.com/wjsoj/cc-core/kiroauth"
)

func TestStoreCRUDRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatal("expected empty store")
	}

	cred := kiroauth.Credentials{
		AccessToken:  "aoaAAAAAFAKE",
		RefreshToken: "aorAAAAAFAKE",
		ProfileARN:   "arn:test",
		ExpiresAt:    "2030-01-01T00:00:00Z",
		AuthMethod:   kiroauth.AuthSocial,
	}
	e, err := s.Add("alice@example", cred)
	if err != nil {
		t.Fatal(err)
	}
	if e.ID == "" || e.Label != "alice@example" {
		t.Fatalf("Add: %+v", e)
	}

	// Re-add with same cred → replaces in place, preserves label when omitted.
	e2, _ := s.Add("", cred)
	if e2.ID != e.ID {
		t.Fatal("same cred should yield same ID")
	}
	if e2.Label != "alice@example" {
		t.Fatalf("empty label should preserve old: %q", e2.Label)
	}

	// Persistence
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Get(e.ID)
	if !ok || got.Cred.AccessToken != "aoaAAAAAFAKE" {
		t.Fatalf("rehydrate: ok=%v got=%+v", ok, got)
	}

	// Update fields
	newLabel := "bob"
	newGroup := "kiro-anthropic"
	disabled := true
	_, err = s2.Update(e.ID, &newLabel, &newGroup, &disabled)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = s2.Get(e.ID)
	if got.Label != "bob" || got.Group != "kiro-anthropic" || !got.Disabled {
		t.Fatalf("Update: %+v", got)
	}
	if hs := s2.HealthyEntries(); len(hs) != 0 {
		t.Fatalf("disabled should not appear in HealthyEntries: %d", len(hs))
	}

	// UpdateCredentials
	cred.AccessToken = "aoaAAAAANEWTOKEN"
	_ = s2.UpdateCredentials(e.ID, cred)
	got, _ = s2.Get(e.ID)
	if got.Cred.AccessToken != "aoaAAAAANEWTOKEN" {
		t.Fatal("UpdateCredentials failed")
	}

	// Delete
	if err := s2.Delete(e.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Get(e.ID); ok {
		t.Fatal("still present after Delete")
	}
	// File gone
	if _, err := openIfExists(filepath.Join(dir, e.ID+".json")); err == nil {
		t.Fatal("file should be gone")
	}
}

func TestMaskedToken(t *testing.T) {
	e := Entry{Cred: kiroauth.Credentials{AccessToken: "aoaAAAAA1234567890longtail"}}
	got := e.MaskedToken()
	if got[:10] != "aoaAAAAA12" || !contains(got, "•") {
		t.Fatalf("MaskedToken: %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(s) > 0 && (s[0:1] == sub || (len(s) > 1 && contains(s[1:], sub)))))
}

func openIfExists(p string) (any, error) {
	return nil, errCheck(p)
}
func errCheck(p string) error {
	// returns error iff missing
	if _, err := filepathStat(p); err != nil {
		return err
	}
	return nil
}

// stub to avoid importing os in test header above
func filepathStat(p string) (any, error) {
	return statHelper(p)
}
