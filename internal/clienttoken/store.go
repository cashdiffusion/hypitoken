// Package clienttoken is the runtime store of client access tokens that
// the proxy accepts in Authorization: Bearer.
//
// Tokens live in a single file, tokens.json, sitting next to state.json.
// The admin panel owns full CRUD.
package clienttoken

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wjsoj/cc-core/auth"
)

// Token is one client access token.
type Token struct {
	Token         string    `json:"token"`
	Name          string    `json:"name"`
	WeeklyUSD     float64   `json:"weekly_usd,omitempty"`
	MaxConcurrent int       `json:"max_concurrent,omitempty"` // 0 = use global default
	RPM           int       `json:"rpm,omitempty"`            // 0 = use global default
	Group         string    `json:"group,omitempty"`          // credential group scope; empty = public
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

// View is the API representation returned to the admin panel.
type View struct {
	Token         string    `json:"token"`
	Name          string    `json:"name"`
	WeeklyUSD     float64   `json:"weekly_usd"`
	MaxConcurrent int       `json:"max_concurrent,omitempty"`
	RPM           int       `json:"rpm,omitempty"`
	Group         string    `json:"group,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

type Store struct {
	mu      sync.RWMutex
	tokens  []Token
	path    string
}

// Open loads tokens.json (if it exists). path may be "" to disable
// persistence (tokens stay in memory only).
func Open(path string) (*Store, error) {
	s := &Store{path: path}

	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	var file struct {
		Tokens []Token `json:"tokens"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, t := range file.Tokens {
		t.Token = strings.TrimSpace(t.Token)
		if t.Token == "" {
			continue
		}
		t.Group = auth.NormalizeGroup(t.Group)
		s.tokens = append(s.tokens, t)
	}
	return s, nil
}

// Lookup reports whether tok is a known client token.
func (s *Store) Lookup(tok string) (name string, weekly float64, maxConc int, group string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tokens {
		if t.Token == tok {
			return t.Name, t.WeeklyUSD, t.MaxConcurrent, t.Group, true
		}
	}
	return "", 0, 0, "", false
}

// RPM returns the per-token RPM override if one is configured. Returns
// (0, false) when the token is unknown or has no override set.
func (s *Store) RPM(tok string) (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tokens {
		if t.Token == tok {
			return t.RPM, true
		}
	}
	return 0, false
}

// Empty reports whether the proxy should run in open mode.
func (s *Store) Empty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tokens) == 0
}

// List returns every token as a View. Safe to serialize to the admin
// panel; do not leak to unauthenticated callers.
func (s *Store) List() []View {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]View, 0, len(s.tokens))
	for _, t := range s.tokens {
		out = append(out, View{
			Token: t.Token, Name: t.Name, WeeklyUSD: t.WeeklyUSD,
			MaxConcurrent: t.MaxConcurrent, RPM: t.RPM,
			Group: t.Group, CreatedAt: t.CreatedAt,
		})
	}
	return out
}

// Add creates a new token. Fails if one with the same value already exists.
func (s *Store) Add(t Token) error {
	t.Token = strings.TrimSpace(t.Token)
	if t.Token == "" {
		return fmt.Errorf("token required")
	}
	if t.WeeklyUSD < 0 {
		t.WeeklyUSD = 0
	}
	t.Name = strings.TrimSpace(t.Name)
	t.Group = auth.NormalizeGroup(t.Group)
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.tokens {
		if existing.Token == t.Token {
			return fmt.Errorf("token already exists")
		}
	}
	s.tokens = append(s.tokens, t)
	return s.saveLocked()
}

// Update patches an existing token. nil fields mean "no change".
func (s *Store) Update(token string, name *string, weekly *float64, maxConc *int, rpm *int, group *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.tokens {
		if s.tokens[i].Token == token {
			if name != nil {
				s.tokens[i].Name = strings.TrimSpace(*name)
			}
			if weekly != nil {
				w := *weekly
				if w < 0 {
					w = 0
				}
				s.tokens[i].WeeklyUSD = w
			}
			if maxConc != nil {
				mc := *maxConc
				if mc < 0 {
					mc = 0
				}
				s.tokens[i].MaxConcurrent = mc
			}
			if rpm != nil {
				r := *rpm
				if r < 0 {
					r = 0
				}
				s.tokens[i].RPM = r
			}
			if group != nil {
				s.tokens[i].Group = auth.NormalizeGroup(*group)
			}
			return s.saveLocked()
		}
	}
	return fmt.Errorf("token not found")
}

// Reset swaps the token string of an existing entry while keeping all
// other fields (name, weekly limit, group, created_at). Useful for
// rotating a leaked secret without losing the row's identity. Refuses
// to clobber another existing token.
func (s *Store) Reset(oldToken, newToken string) error {
	newToken = strings.TrimSpace(newToken)
	if newToken == "" {
		return fmt.Errorf("new token required")
	}
	if oldToken == newToken {
		return fmt.Errorf("new token must differ from old")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, t := range s.tokens {
		if t.Token == oldToken {
			idx = i
		}
		if t.Token == newToken {
			return fmt.Errorf("new token already exists")
		}
	}
	if idx < 0 {
		return fmt.Errorf("token not found")
	}
	s.tokens[idx].Token = newToken
	return s.saveLocked()
}

// Delete removes a token.
func (s *Store) Delete(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.tokens {
		if t.Token == token {
			s.tokens = append(s.tokens[:i], s.tokens[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("token not found")
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	payload := struct {
		Tokens []Token `json:"tokens"`
	}{Tokens: s.tokens}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Generate returns a fresh token in the form sk-<48 alphanumerics>.
func Generate() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	const n = 48
	max := big.NewInt(int64(len(alphabet)))
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[v.Int64()]
	}
	return "sk-" + string(b), nil
}
