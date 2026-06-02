// Package kirocreds is the hypitoken-side wrapper around cc-core's
// kiroauth.File. It serves three jobs:
//
//   - Persist per-credential files (one .json per Kiro account) so the admin
//     can add/list/delete them without manipulating raw cc-core files;
//   - Mint stable IDs that the admin panel can address;
//   - Drive PKCE login flow from the admin UI (kick + finish).
//
// One Kiro credential file = one logical account. cc-core's kiroauth.File
// natively supports multi-credential arrays, but for the admin UX we keep
// one file per account so naming / deletion is unambiguous.
package kirocreds

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wjsoj/cc-core/kiroauth"
)

// Entry is one stored Kiro credential.
type Entry struct {
	ID         string                `json:"id"`
	Label      string                `json:"label,omitempty"`
	Group      string                `json:"group,omitempty"` // for future per-credential group scoping
	Disabled   bool                  `json:"disabled,omitempty"`
	// MaxConcurrent is the per-credential in-flight request cap. 0 = unlimited.
	// Enforced by Store.Acquire/Release; the live active counter lives in
	// Store.active (process-local, not persisted).
	MaxConcurrent int                `json:"max_concurrent,omitempty"`
	CreatedAt  time.Time             `json:"created_at"`
	UpdatedAt  time.Time             `json:"updated_at,omitempty"`
	Cred       kiroauth.Credentials  `json:"credentials"`
}

// MaskedToken returns Cred.AccessToken with all but the first 10 chars masked.
// Safe to surface in the admin UI.
func (e *Entry) MaskedToken() string {
	t := e.Cred.AccessToken
	if len(t) <= 10 {
		return strings.Repeat("•", len(t))
	}
	return t[:10] + strings.Repeat("•", 6)
}

// Store manages a directory of one-credential-per-file Kiro entries.
//
// Layout: <dir>/<id>.json holds {label, group, disabled, credentials:{…}}.
//
// All public methods are concurrency-safe.
type Store struct {
	mu      sync.RWMutex
	dir     string
	entries map[string]*Entry // keyed by ID
	// active tracks the in-flight request count per entry. Process-local —
	// never persisted to disk. Pointer so Acquire/Release use atomics
	// without holding the store mutex.
	active map[string]*int64
}

// Open scans dir for *.json credential files. dir is created if missing.
// Returns a usable Store even on a fresh directory (no entries).
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("kirocreds: empty dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("kirocreds: mkdir %s: %w", dir, err)
	}
	s := &Store{dir: dir, entries: make(map[string]*Entry), active: make(map[string]*int64)}
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("kirocreds: glob: %w", err)
	}
	for _, p := range matches {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var e Entry
		if err := json.Unmarshal(data, &e); err != nil {
			continue
		}
		if e.ID == "" {
			e.ID = strings.TrimSuffix(filepath.Base(p), ".json")
		}
		s.entries[e.ID] = &e
	}
	return s, nil
}

// List returns all entries sorted by CreatedAt (oldest first).
// The returned slice is a defensive copy; mutating it is safe.
func (s *Store) List() []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Entry, 0, len(s.entries))
	for _, e := range s.entries {
		copy := *e
		out = append(out, &copy)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Get returns the entry by ID, or (nil, false).
func (s *Store) Get(id string) (*Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[id]
	if !ok {
		return nil, false
	}
	copy := *e
	return &copy, true
}

// HealthyEntries returns enabled entries whose AccessToken is non-empty.
// Caller-side refresh is still required (use Refresh below).
func (s *Store) HealthyEntries() []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Entry
	for _, e := range s.entries {
		if e.Disabled || e.Cred.AccessToken == "" {
			continue
		}
		copy := *e
		out = append(out, &copy)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Add stores a freshly-issued credential, deriving its ID from the access
// token (so re-running PKCE for the same account replaces the entry).
func (s *Store) Add(label string, cred kiroauth.Credentials) (*Entry, error) {
	if cred.AccessToken == "" {
		return nil, errors.New("kirocreds: cred missing accessToken")
	}
	id := deriveID(cred)
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, replacing := s.entries[id]
	e := &Entry{
		ID:        id,
		Label:     strings.TrimSpace(label),
		CreatedAt: now,
		UpdatedAt: now,
		Cred:      cred,
	}
	if replacing {
		// Preserve user-tunable fields across re-login.
		e.CreatedAt = existing.CreatedAt
		e.Label = orDefault(label, existing.Label)
		e.Group = existing.Group
		e.Disabled = existing.Disabled
	}
	if err := s.saveEntryLocked(e); err != nil {
		return nil, err
	}
	s.entries[id] = e
	return e, nil
}

// Update patches mutable metadata fields. nil fields leave them unchanged.
func (s *Store) Update(id string, label *string, group *string, disabled *bool, maxConcurrent *int) (*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return nil, fmt.Errorf("kirocreds: id %q not found", id)
	}
	if label != nil {
		e.Label = strings.TrimSpace(*label)
	}
	if group != nil {
		e.Group = strings.TrimSpace(*group)
	}
	if disabled != nil {
		e.Disabled = *disabled
	}
	if maxConcurrent != nil {
		if *maxConcurrent < 0 {
			e.MaxConcurrent = 0
		} else {
			e.MaxConcurrent = *maxConcurrent
		}
	}
	e.UpdatedAt = time.Now().UTC()
	if err := s.saveEntryLocked(e); err != nil {
		return nil, err
	}
	copy := *e
	return &copy, nil
}

// Acquire reserves an in-flight slot on the given entry. Returns false when
// the entry already has Entry.MaxConcurrent requests in flight (0 = unlimited
// always succeeds). Caller MUST pair with Release on success.
func (s *Store) Acquire(id string) bool {
	s.mu.RLock()
	e, ok := s.entries[id]
	if !ok {
		s.mu.RUnlock()
		return false
	}
	cap := e.MaxConcurrent
	counter := s.active[id]
	s.mu.RUnlock()
	if counter == nil {
		s.mu.Lock()
		counter = s.active[id]
		if counter == nil {
			var n int64
			counter = &n
			s.active[id] = counter
		}
		s.mu.Unlock()
	}
	if cap <= 0 {
		atomic.AddInt64(counter, 1)
		return true
	}
	for {
		cur := atomic.LoadInt64(counter)
		if cur >= int64(cap) {
			return false
		}
		if atomic.CompareAndSwapInt64(counter, cur, cur+1) {
			return true
		}
	}
}

// Release returns an in-flight slot. No-op when the entry is unknown.
func (s *Store) Release(id string) {
	s.mu.RLock()
	counter := s.active[id]
	s.mu.RUnlock()
	if counter == nil {
		return
	}
	if v := atomic.AddInt64(counter, -1); v < 0 {
		// Defensive: should never go negative; reset to 0.
		atomic.StoreInt64(counter, 0)
	}
}

// Active reports the current in-flight count for the entry. 0 when unknown.
func (s *Store) Active(id string) int64 {
	s.mu.RLock()
	counter := s.active[id]
	s.mu.RUnlock()
	if counter == nil {
		return 0
	}
	return atomic.LoadInt64(counter)
}

// UpdateCredentials replaces the embedded Credentials (typically after a
// refresh rotated the refresh_token). Persists atomically.
func (s *Store) UpdateCredentials(id string, cred kiroauth.Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return fmt.Errorf("kirocreds: id %q not found", id)
	}
	e.Cred = cred
	e.UpdatedAt = time.Now().UTC()
	return s.saveEntryLocked(e)
}

// Delete removes the entry from disk and memory.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[id]; !ok {
		return fmt.Errorf("kirocreds: id %q not found", id)
	}
	delete(s.entries, id)
	return os.Remove(s.entryPath(id))
}

func (s *Store) entryPath(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) saveEntryLocked(e *Entry) error {
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := s.entryPath(e.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.entryPath(e.ID))
}

// deriveID is sha256(refresh_token || access_token)[:12]. Stable across
// re-logins of the same account (refresh token rotates with each refresh,
// but a fresh PKCE login mints a new one, so deriving from refresh token
// alone would not de-dup). Using both is safer.
func deriveID(c kiroauth.Credentials) string {
	h := sha256.New()
	h.Write([]byte(c.AccessToken))
	h.Write([]byte("|"))
	h.Write([]byte(c.RefreshToken))
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func orDefault(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
