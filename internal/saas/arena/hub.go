package arena

import "sync"

// Event is one activity pulse — a billed request landing for some user. It is
// fanned out to every live SSE subscriber and used by the office renderer to
// animate that user's character. The real user id is never serialised; the
// client only ever sees the opaque ActorID.
type Event struct {
	ActorID  string `json:"actor"`    // opaque stable per-user grouping token
	Name     string `json:"name"`     // resolved nickname or pseudonym
	Public   bool   `json:"public"`   // true = real nickname, false = pseudonym
	Provider string `json:"provider"` // "anthropic" | "openai" | …
	Model    string `json:"model"`
	Tokens   int64  `json:"tokens"` // total tokens for this request
	TS       int64  `json:"ts"`     // unix milliseconds

	userID int64 // unexported — for per-subscriber is_you marking, not serialised
}

// Hub is an in-memory pub/sub fan-out with a small replay ring buffer. It never
// blocks the publisher: a subscriber whose buffer is full simply drops the
// event (the office is a best-effort live view, not a durable log).
type Hub struct {
	mu       sync.RWMutex
	subs     map[int]chan Event
	nextID   int
	ring     []Event
	ringSize int
}

// NewHub creates a hub retaining the last `ringSize` events for replay to
// freshly-connected subscribers.
func NewHub(ringSize int) *Hub {
	if ringSize <= 0 {
		ringSize = 30
	}
	return &Hub{subs: make(map[int]chan Event), ringSize: ringSize}
}

// Subscribe registers a new subscriber and returns its channel plus an
// unsubscribe id. Buffer is generous so a briefly-stalled client tolerates a
// burst before events start dropping.
func (h *Hub) Subscribe() (int, <-chan Event) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	id := h.nextID
	h.nextID++
	h.subs[id] = ch
	h.mu.Unlock()
	return id, ch
}

// Unsubscribe removes and closes a subscriber's channel.
func (h *Hub) Unsubscribe(id int) {
	h.mu.Lock()
	if ch, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(ch)
	}
	h.mu.Unlock()
}

// Recent returns a copy of the replay ring (oldest first) so a new connection
// can paint the office with the last few moments of activity immediately.
func (h *Hub) Recent() []Event {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Event, len(h.ring))
	copy(out, h.ring)
	return out
}

// Publish fans an event out to all subscribers (non-blocking) and appends it to
// the replay ring.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	h.ring = append(h.ring, e)
	if len(h.ring) > h.ringSize {
		h.ring = h.ring[len(h.ring)-h.ringSize:]
	}
	// Snapshot subscriber channels under the lock, send outside it.
	chans := make([]chan Event, 0, len(h.subs))
	for _, ch := range h.subs {
		chans = append(chans, ch)
	}
	h.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- e:
		default: // subscriber lagging — drop this event for them
		}
	}
}

// SubscriberCount reports how many live subscribers there are (test/diagnostic).
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}
