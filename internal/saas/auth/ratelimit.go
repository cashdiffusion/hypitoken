package auth

import (
	"sync"
	"time"
)

// codeRateLimiter prevents email-bomb / SMTP-quota abuse on /auth/send-code.
//
// Two parallel sliding-window counters per call:
//   - per-email   max 1 / 60s   AND  max 5 / hour
//   - per-IP      max 10 / hour AND  max 50 / day
//
// Reasoning: the email-side limit protects the user (no spam to their inbox);
// the IP-side limit protects the operator's SMTP quota. Both must pass.
//
// In-memory only — fine for single-process deployments. For a multi-replica
// setup behind a load-balancer, swap this for a Redis-backed implementation.
type codeRateLimiter struct {
	mu     sync.Mutex
	events map[string][]time.Time // key → timestamps within the longest window
}

func newCodeRateLimiter() *codeRateLimiter {
	return &codeRateLimiter{events: make(map[string][]time.Time)}
}

// allow checks both per-email and per-IP windows. Returns (ok, retryAfterSeconds).
func (l *codeRateLimiter) allow(email, ip string) (bool, int) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gcLocked(now)

	// Email: 1/60s, 5/hour
	if ok, retry := check(l.events, "e:"+email, now, []limit{
		{1, time.Minute},
		{5, time.Hour},
	}); !ok {
		return false, retry
	}
	// IP: 10/hour, 50/day. Empty IP (loopback / dev) is exempt — protects
	// localhost smoke tests from accidentally hitting the cap.
	if ip != "" {
		if ok, retry := check(l.events, "i:"+ip, now, []limit{
			{10, time.Hour},
			{50, 24 * time.Hour},
		}); !ok {
			return false, retry
		}
	}

	// Record the event under both keys.
	l.events["e:"+email] = append(l.events["e:"+email], now)
	if ip != "" {
		l.events["i:"+ip] = append(l.events["i:"+ip], now)
	}
	return true, 0
}

type limit struct {
	max    int
	window time.Duration
}

func check(m map[string][]time.Time, key string, now time.Time, limits []limit) (bool, int) {
	stamps := m[key]
	for _, lim := range limits {
		cutoff := now.Add(-lim.window)
		count := 0
		var oldest time.Time
		for _, t := range stamps {
			if t.After(cutoff) {
				if count == 0 || t.Before(oldest) {
					oldest = t
				}
				count++
			}
		}
		if count >= lim.max {
			retry := int(oldest.Add(lim.window).Sub(now).Seconds())
			if retry < 1 {
				retry = 1
			}
			return false, retry
		}
	}
	return true, 0
}

// gcLocked drops entries older than 24h (the longest window we track).
func (l *codeRateLimiter) gcLocked(now time.Time) {
	cutoff := now.Add(-24 * time.Hour)
	for k, ts := range l.events {
		kept := ts[:0]
		for _, t := range ts {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(l.events, k)
		} else {
			l.events[k] = kept
		}
	}
}
