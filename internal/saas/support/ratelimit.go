package support

import (
	"sync"
	"time"
)

// rateLimiter guards the unauthenticated appeal endpoint. With the emailed OTP
// gone, this is the ONLY thing standing between an open POST and a spam sink —
// anyone can submit, under any address, with no proof of anything.
//
// Windows, per key:
//
//	submit  email 3/hour, IP 20/day
//
// The IP window is a hard daily ceiling rather than an hourly one: an attacker
// rotating addresses is bounded by their IP, and 20 appeals a day from one
// address is already far past what a person with a genuine grievance files.
// The email window stays loose because someone appealing a wrongful ban will
// legitimately retry, and locking them out of the only channel they have left
// is the one failure mode worse than some spam. In-memory, like auth's —
// single-process deployment.
type rateLimiter struct {
	mu     sync.Mutex
	events map[string][]time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{events: make(map[string][]time.Time)}
}

type window struct {
	n int
	d time.Duration
}

// allowSubmit gates ticket creation and replies. Returns (ok, retryAfterSeconds).
func (l *rateLimiter) allowSubmit(email, ip string) (bool, int) {
	return l.allow("submit", email, ip,
		[]window{{3, time.Hour}},
		[]window{{20, 24 * time.Hour}})
}

func (l *rateLimiter) allow(scope, email, ip string, emailWins, ipWins []window) (bool, int) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gcLocked(now)

	if ok, retry := check(l.events, scope+":e:"+email, now, emailWins); !ok {
		return false, retry
	}
	// An empty IP (loopback / dev) is exempt so local smoke tests don't trip it.
	if ip != "" {
		if ok, retry := check(l.events, scope+":i:"+ip, now, ipWins); !ok {
			return false, retry
		}
	}
	// Only record the hit once every window passed, so a rejected call doesn't
	// extend its own penalty.
	l.events[scope+":e:"+email] = append(l.events[scope+":e:"+email], now)
	if ip != "" {
		l.events[scope+":i:"+ip] = append(l.events[scope+":i:"+ip], now)
	}
	return true, 0
}

// check reports whether key is under every window. Returns the seconds until the
// tightest breached window frees up.
func check(events map[string][]time.Time, key string, now time.Time, wins []window) (bool, int) {
	stamps := events[key]
	for _, w := range wins {
		cutoff := now.Add(-w.d)
		n := 0
		var oldest time.Time
		for _, ts := range stamps {
			if ts.After(cutoff) {
				n++
				if oldest.IsZero() || ts.Before(oldest) {
					oldest = ts
				}
			}
		}
		if n >= w.n {
			retry := int(oldest.Add(w.d).Sub(now).Seconds()) + 1
			if retry < 1 {
				retry = 1
			}
			return false, retry
		}
	}
	return true, 0
}

// gcLocked drops timestamps older than the longest window we track (24h), so
// the map cannot grow without bound across a long-running process.
func (l *rateLimiter) gcLocked(now time.Time) {
	cutoff := now.Add(-24 * time.Hour)
	for k, stamps := range l.events {
		kept := stamps[:0]
		for _, ts := range stamps {
			if ts.After(cutoff) {
				kept = append(kept, ts)
			}
		}
		if len(kept) == 0 {
			delete(l.events, k)
			continue
		}
		l.events[k] = kept
	}
}
