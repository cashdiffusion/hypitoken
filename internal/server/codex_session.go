package server

import (
	"sync"
	"time"

	"github.com/wjsoj/cc-core/mimicry"
)

// codexRespAccountTTL bounds how long a response->account binding survives. A
// Codex conversation that idles longer than this rotates its sticky account on
// the next turn (the upstream rebuilds from full input), which is acceptable.
const codexRespAccountTTL = time.Hour

type codexRespAccountEntry struct {
	authID string
	exp    time.Time
}

// codexRespAccountStore binds a Codex response id to the credential (authID)
// that produced it, namespaced by credential group. This is the multi-tenant
// safety boundary for previous_response_id continuation: a response chain minted
// under group A must never resolve a sticky account in group B — otherwise one
// tenant's conversation can be replayed against another account, which the
// upstream rejects as a session-auth mismatch ("跨组会话串号"). Mirrors sub2api's
// {groupID}:{responseID} keyspace (commits 87dd5f5d / 9a0e4398), minus the Redis
// layer since hypitoken is single-process.
type codexRespAccountStore struct {
	mu      sync.RWMutex
	entries map[string]codexRespAccountEntry
	ttl     time.Duration
	stop    chan struct{}
}

func newCodexRespAccountStore(ttl time.Duration) *codexRespAccountStore {
	if ttl <= 0 {
		ttl = codexRespAccountTTL
	}
	s := &codexRespAccountStore{
		entries: make(map[string]codexRespAccountEntry),
		ttl:     ttl,
		stop:    make(chan struct{}),
	}
	go s.janitor()
	return s
}

func codexRespKey(group, respID string) string { return group + "|" + respID }

// Bind records that respID (within group) was produced by authID.
func (s *codexRespAccountStore) Bind(group, respID, authID string) {
	if respID == "" || authID == "" {
		return
	}
	s.mu.Lock()
	s.entries[codexRespKey(group, respID)] = codexRespAccountEntry{authID: authID, exp: time.Now().Add(s.ttl)}
	s.mu.Unlock()
}

// Get returns the authID bound to (group, respID) within this group, and whether
// it was found and unexpired. A miss means the response chain does not belong to
// this group's sticky account — the caller must strip previous_response_id.
func (s *codexRespAccountStore) Get(group, respID string) (string, bool) {
	if respID == "" {
		return "", false
	}
	s.mu.RLock()
	e, ok := s.entries[codexRespKey(group, respID)]
	s.mu.RUnlock()
	if !ok || time.Now().After(e.exp) {
		return "", false
	}
	return e.authID, true
}

func (s *codexRespAccountStore) janitor() {
	t := time.NewTicker(s.ttl)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			now := time.Now()
			s.mu.Lock()
			for k, e := range s.entries {
				if now.After(e.exp) {
					delete(s.entries, k)
				}
			}
			s.mu.Unlock()
		}
	}
}

// Close stops the background janitor. Safe to call once.
func (s *codexRespAccountStore) Close() { close(s.stop) }

// codexPreviousResponseID extracts previous_response_id from a /v1/responses
// body, or "" if absent.
//
// Delegates to cc-core, which reads the key off a top-level scan rather than a
// full unmarshal — a Codex frame embeds arbitrary user prose, and a client
// quoting `"previous_response_id"` in a prompt must not be able to steer this.
func codexPreviousResponseID(body []byte) string {
	return mimicry.CodexPreviousResponseID(body)
}

// removeCodexPreviousResponseID strips previous_response_id so the upstream
// rebuilds context from the full input. Used on cross-group session mismatch
// (the chain doesn't belong to this group's sticky account).
//
// This used to unmarshal → delete → marshal, which made Go re-emit the frame's
// top-level keys in SORTED order. Codex's own key order is stable across every
// captured frame and is part of the shape we imitate, so the round-trip quietly
// undid the byte fidelity the rest of the WS path maintains. cc-core's version
// cuts the member out of the bytes.
func removeCodexPreviousResponseID(body []byte) []byte {
	return mimicry.RemoveCodexPreviousResponseID(body)
}
