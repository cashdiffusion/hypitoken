package server

import (
	"sync"
	"time"

	"github.com/wjsoj/cc-core/auth"
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

// codexUpstreamSessionID resolves the stable `session-id` this request should
// present upstream.
//
// The backend places a conversation in its prompt cache by this header, so it
// has to survive every turn of that conversation. It did not: the HTTP path
// minted a fresh UUID per REQUEST, which was invisible for as long as cc-core
// misspelled the header as `Session_id` (the backend ignores a name no client
// sends) and became expensive the moment the spelling was corrected — Codex
// cache hit rate fell from ~87% to ~45%, with a third of all turns arriving at
// cache_read == 0 while carrying more than 10k of context.
//
// The anchor names one conversation on one credential:
//
//   - accountKey pins it to the credential, so a failover correctly starts a
//     new upstream session rather than replaying one account's id on another.
//   - clientToken separates tenants.
//   - the conversation key separates concurrent conversations within a tenant.
//     conversationAnchor reads the client's own prompt_cache_key first, which
//     is exactly the id a real Codex client uses for both fields, so the two
//     agree the way they do for a genuine client. slotID is the fallback for a
//     body that names no conversation — coarser (a sessionless relay shares one
//     fan-out bucket across several conversations, see relay_fanout.go), but
//     still stable per turn, which is the property that matters here.
//
// Both anchor components below the account are downstream-supplied, which is
// why neither is ever used as the session id itself: the id is the upstream
// prompt-cache namespace, and a caller able to choose it could aim at another
// tenant's cached prefix. Hashing them under an accountKey + clientToken prefix
// confines each caller to its own keyspace. Same reasoning as the WS path's
// anchor in handleCodexResponsesWS.
func (s *Server) codexUpstreamSessionID(a *auth.Auth, clientToken, slotID string, body []byte) string {
	conv, _ := conversationAnchor(body)
	if conv == "" {
		conv = slotID
	}
	return s.codexSessions.SessionID(a.AccountKey() + "|" + clientToken + "|" + conv)
}
