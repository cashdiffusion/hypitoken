package server

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Bounded fan-out slots for callers that declare no session of their own.
//
// The pool schedules on (provider, client token, slot). A caller that sends no
// session header collapses to the empty slot, which means ONE slot for the
// whole token — and since a slot is sticky-assigned, one upstream credential
// serves everything that token sends, for as long as that credential stays
// healthy.
//
// For a raw curl caller that is the right answer. For a third-party relay
// (new-api and friends) it is not: the relay is many independent end users
// behind one token, and it does not speak the cc-core/relay protocol that
// trusted_relay_tokens keys on, so relayIdentity cannot help. Production has
// measured a single such token at 25% of all Codex traffic pinned to one
// credential for hours at a stretch, which costs three ways:
//
//   - the credential absorbs the relay's entire request rate while the rest of
//     the fleet idles, and it is the request rate on one account that draws
//     upstream capacity sheds;
//   - every one of the relay's users shares a single upstream session and
//     therefore a single prompt_cache_key, so unrelated conversations
//     interleave in one cache and the hit rate drops (measured 46.5% for the
//     relay vs 56.6% for real CLI clients);
//   - that shape — one session carrying dozens of concurrent unrelated threads
//     — is one no genuine client produces, which is exactly the kind of signal
//     third-party detection looks for.
//
// So when the transport gives us no session, we recover one from the request
// body, which the relay does forward verbatim. Real Codex clients carry
// prompt_cache_key (== their session id) and client_metadata.session_id;
// failing that, the first user message identifies a conversation well enough
// and stays constant across its turns.
//
// The anchor is then hashed into a FIXED number of buckets rather than used
// directly. Bucket count is the whole point: a slot costs pool capacity
// (MaxConcurrent counts distinct sessions, and a session lingers for the whole
// ActiveWindow after its last request), so minting one slot per conversation
// would let a busy relay claim more slots than the fleet has and push everyone
// onto API keys or 503s. Hashing bounds the cost at sessionlessFanoutWidth
// slots per token while keeping each conversation on a stable bucket — and so
// on a stable credential, which is what preserves the prompt cache.
//
// With no anchor at all the result is "", i.e. exactly the old behaviour.

// sessionlessFanoutWidth is how many scheduler slots one sessionless client
// token may occupy. 8 spreads a relay across up to 8 credentials while leaving
// the fleet's slot budget intact even with several such tokens active.
const sessionlessFanoutWidth = 8

// fanoutSlotID maps a sessionless request onto one of sessionlessFanoutWidth
// stable buckets, or returns "" when the body carries nothing that identifies a
// conversation.
func fanoutSlotID(body []byte, width int) string {
	if width <= 1 {
		return ""
	}
	anchor, src := conversationAnchor(body)
	fanoutStats.record(src)
	if anchor == "" {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(anchor))
	return "fanout-" + strconv.FormatUint(h.Sum64()%uint64(width), 10)
}

// anchorPeek is the minimal view of a request body needed to name the
// conversation it belongs to. Fields cover both provider dialects; whichever is
// present wins in conversationAnchor's documented order.
type anchorPeek struct {
	// Codex: prompt_cache_key is the client's own session id and is present on
	// every real request, which makes it the strongest anchor available.
	PromptCacheKey string `json:"prompt_cache_key"`
	ClientMetadata struct {
		SessionID string `json:"session_id"`
		ThreadID  string `json:"thread_id"`
	} `json:"client_metadata"`
	ConversationID string `json:"conversation_id"`

	// PreviousResponseID marks a turn that continues a server-side response
	// chain. Such a turn carries only the delta, so the opening message is not
	// in the body and the content fallback below would name a different
	// conversation every turn — see conversationAnchor.
	PreviousResponseID string `json:"previous_response_id"`

	// Anthropic: metadata.user_id is per-conversation for real Claude Code.
	Metadata struct {
		UserID string `json:"user_id"`
	} `json:"metadata"`

	// Content fallback. /v1/responses calls it input (and allows a bare
	// string), /v1/messages and /v1/chat/completions call it messages.
	Input    json.RawMessage `json:"input"`
	Messages json.RawMessage `json:"messages"`
}

// anchorItem is one entry of an input/messages array. content is kept raw: it
// is hashed, never interpreted, so every content dialect (string, block array,
// tool output) works without a case for each.
type anchorItem struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// conversationAnchor names the conversation a request belongs to, or "" when
// the body says nothing about it.
//
// Order matters: an explicit client-supplied session id is preferred over
// content because it survives compaction, tool loops and continuation turns
// that no longer carry the opening message. The content hash is the last
// resort, and deliberately keys on the FIRST user turn — a conversation's
// opening message is stable for its whole life, while the latest one changes
// every turn and would rebucket (and so reschedule) mid-conversation.
//
// System/developer instructions are skipped on purpose: they are identical
// across every user of a given client, so anchoring on them would put all of
// them back in one bucket — the exact collapse this is here to undo.
//
// The content fallback is withheld from a previous_response_id continuation.
// Those turns carry only the new delta, so hashing their content would name a
// fresh conversation every turn and reschedule the thread onto a different
// credential mid-flight — which for a response chain is not merely a lost
// cache but a broken request, since the id was minted by, and is only valid
// on, the account that produced it. Left anchorless, such a caller keeps the
// single sticky slot it has always had.
func conversationAnchor(body []byte) (string, anchorSource) {
	if len(body) == 0 {
		return "", anchorNone
	}
	var p anchorPeek
	if json.Unmarshal(body, &p) != nil {
		return "", anchorNone
	}
	if p.PromptCacheKey != "" {
		return p.PromptCacheKey, anchorCacheKey
	}
	for _, id := range []string{
		p.ClientMetadata.SessionID,
		p.ClientMetadata.ThreadID,
		p.ConversationID,
		p.Metadata.UserID,
	} {
		if id != "" {
			return id, anchorMetadata
		}
	}
	if p.PreviousResponseID != "" {
		return "", anchorChainNoSession
	}
	for _, raw := range []json.RawMessage{p.Input, p.Messages} {
		if a := firstUserContent(raw); a != "" {
			return a, anchorContent
		}
	}
	return "", anchorNone
}

// firstUserContent returns the raw content of the first user-authored entry of
// an input/messages value, or "" if there is none. A bare string input (the
// one-shot /v1/responses form) is itself the user's message.
func firstUserContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var items []anchorItem
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	for _, it := range items {
		if it.Role == "user" && len(it.Content) > 0 {
			return string(it.Content)
		}
	}
	return ""
}

// Anchor-source accounting. Whether the fan-out is doing anything at all
// depends entirely on what the caller's body happens to carry, which is not
// visible from the request log: a slot is not recorded there, so a token whose
// bodies yield no anchor looks exactly like one that fans out and happens to be
// quiet. Counting the sources and reporting them periodically is what makes the
// difference observable — and tells us which anchor is actually load-bearing in
// production, rather than which one we assumed would be.
type anchorSource int

const (
	anchorNone anchorSource = iota
	anchorCacheKey
	anchorMetadata
	anchorContent
	anchorChainNoSession
)

var anchorSourceNames = [...]string{"none", "prompt_cache_key", "client_metadata", "first_user_message", "response_chain(no session)"}

type anchorStats struct {
	mu     sync.Mutex
	counts [len(anchorSourceNames)]uint64
	last   time.Time
}

var fanoutStats anchorStats

// record counts one classification and reports the running tallies at most
// once a minute, so a busy proxy pays one log line rather than one per request.
func (s *anchorStats) record(src anchorSource) {
	s.mu.Lock()
	s.counts[src]++
	now := time.Now()
	if s.last.IsZero() {
		s.last = now
	}
	if now.Sub(s.last) < time.Minute {
		s.mu.Unlock()
		return
	}
	snapshot := s.counts
	s.counts = [len(anchorSourceNames)]uint64{}
	s.last = now
	s.mu.Unlock()

	parts := make([]string, 0, len(snapshot))
	var total uint64
	for i, n := range snapshot {
		if n == 0 {
			continue
		}
		total += n
		parts = append(parts, fmt.Sprintf("%s=%d", anchorSourceNames[i], n))
	}
	if total > 0 {
		log.Infof("fanout: sessionless anchors over the last minute: %s", strings.Join(parts, " "))
	}
}
