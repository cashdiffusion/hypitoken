package referral

import (
	"sync"
	"time"
)

// claimLimiter is a small in-memory sliding-window rate limiter for gift redeem
// attempts. Combined with the 80-bit redeem-code entropy it makes brute-force
// enumeration of /referral/gifts/claim infeasible: even at the cap a guesser
// gets a handful of tries a minute against a 2^80 space. Keyed per
// (user, IP) so one abuser can't lock out everyone.
type claimLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	window time.Duration
	max    int
}

func newClaimLimiter() *claimLimiter {
	return &claimLimiter{hits: make(map[string][]time.Time), window: time.Minute, max: 8}
}

// allow records an attempt for key and reports whether it is within the limit.
// When blocked it returns the seconds until the oldest in-window attempt ages
// out (a Retry-After hint).
func (l *claimLimiter) allow(key string) (ok bool, retryAfter int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)
	h := l.hits[key]
	// Drop attempts older than the window.
	i := 0
	for i < len(h) && h[i].Before(cutoff) {
		i++
	}
	h = h[i:]
	if len(h) >= l.max {
		retry := int(l.window.Seconds()-now.Sub(h[0]).Seconds()) + 1
		if retry < 1 {
			retry = 1
		}
		l.hits[key] = h
		return false, retry
	}
	l.hits[key] = append(h, now)
	return true, 0
}

// gc drops empty / fully-aged-out keys so the map can't grow unbounded. Called
// opportunistically (cheap; the map is small).
func (l *claimLimiter) gc() {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-l.window)
	for k, h := range l.hits {
		if len(h) == 0 || h[len(h)-1].Before(cutoff) {
			delete(l.hits, k)
		}
	}
}
