package support

import (
	"sync"
	"time"
)

// rateLimiter guards the two unauthenticated appeal endpoints: the OTP send and
// the submission itself. Both are reachable with no session by design, which is
// exactly what makes them worth abusing — the send path costs the operator SMTP
// quota and can be pointed at someone else's inbox, and the submit path writes
// rows.
//
// Windows, per key:
//
//	send    email 1/60s + 5/hour, IP 10/hour
//	submit  email 3/hour,         IP 10/hour
//
// Deliberately looser than the registration limiter on the email axis and
// tighter on the IP axis: someone appealing a wrongful ban is likely to retry in
// frustration, and locking them out of the only channel they have left would
// defeat the purpose. In-memory, like auth's — single-process deployment.
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

// allowSend gates the OTP mail. Returns (ok, retryAfterSeconds).
func (l *rateLimiter) allowSend(email, ip string) (bool, int) {
	return l.allow("send", email, ip,
		[]window{{1, time.Minute}, {5, time.Hour}},
		[]window{{10, time.Hour}})
}

// allowSubmit gates ticket creation.
func (l *rateLimiter) allowSubmit(email, ip string) (bool, int) {
	return l.allow("submit", email, ip,
		[]window{{3, time.Hour}},
		[]window{{10, time.Hour}})
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
