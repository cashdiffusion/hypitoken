package server

import (
	"sync"
	"time"
)

const (
	claudePreparationFailureWindow = time.Minute
	maxClaudePreparationFailures   = 4096
)

type claudePreparationFailure struct {
	first time.Time
	last  time.Time
	count int
}

// claudePreparationFailureTracker is deliberately a bounded in-memory
// observability aid, not a client-facing rate limiter. Unsafe Generic bodies
// are rejected locally on their first occurrence, so this only annotates
// repeated attempts and cannot become a source of cross-client throttling.
type claudePreparationFailureTracker struct {
	mu      sync.Mutex
	entries map[string]claudePreparationFailure
}

func (t *claudePreparationFailureTracker) record(clientHash, structureHash, reason string, now time.Time) int {
	key := clientHash + "\x00" + structureHash + "\x00" + reason
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = make(map[string]claudePreparationFailure)
	}
	if len(t.entries) >= maxClaudePreparationFailures {
		for candidate, entry := range t.entries {
			if now.Sub(entry.last) > claudePreparationFailureWindow {
				delete(t.entries, candidate)
			}
		}
	}
	entry := t.entries[key]
	if entry.first.IsZero() || now.Sub(entry.first) > claudePreparationFailureWindow {
		entry = claudePreparationFailure{first: now}
	}
	entry.last = now
	entry.count++
	t.entries[key] = entry
	return entry.count
}
