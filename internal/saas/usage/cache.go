package usage

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// summaryTTL is how long a rendered /usage/summary body is reused.
//
// The endpoint runs five aggregations over the same slice of the charge ledger
// and had no cache at all, so every dashboard load, tab focus and refresh
// re-ran all of them — measured at 1.9–3.4 s each on production. Spend figures
// are a reporting view, not a balance check (the wallet gate reads the balance
// directly), so a minute of staleness is invisible to users and turns a burst
// of refreshes into one query pass.
const summaryTTL = 60 * time.Second

// summaryCacheMax bounds the map. Keys are per (scope, window, filters), so a
// workspace with many admins slicing different ranges could otherwise grow it
// without limit. Well above any real working set; the sweep is O(n) and only
// runs when the cap is hit.
const summaryCacheMax = 512

type cacheEntry struct {
	at   time.Time
	body []byte
}

type respCache struct {
	mu sync.Mutex
	m  map[string]cacheEntry
	sf singleflight.Group
}

func newRespCache() *respCache { return &respCache{m: make(map[string]cacheEntry)} }

// get returns a cached body if it is still fresh.
func (rc *respCache) get(key string) ([]byte, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	e, ok := rc.m[key]
	if !ok || time.Since(e.at) >= summaryTTL {
		return nil, false
	}
	return e.body, true
}

func (rc *respCache) put(key string, body []byte) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if len(rc.m) >= summaryCacheMax {
		for k, e := range rc.m {
			if time.Since(e.at) >= summaryTTL {
				delete(rc.m, k)
			}
		}
		// Everything was still fresh — drop the map rather than grow forever.
		if len(rc.m) >= summaryCacheMax {
			rc.m = make(map[string]cacheEntry, summaryCacheMax)
		}
	}
	rc.m[key] = cacheEntry{at: time.Now(), body: body}
}

// do returns the cached body for key, computing it via build on a miss.
// Concurrent misses on the same key collapse into one build.
func (rc *respCache) do(key string, build func() (any, error)) ([]byte, error) {
	if body, ok := rc.get(key); ok {
		return body, nil
	}
	v, err, _ := rc.sf.Do(key, func() (any, error) {
		// Re-check inside singleflight: callers queued behind the winner must
		// not each run the aggregation again.
		if body, ok := rc.get(key); ok {
			return body, nil
		}
		payload, err := build()
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		rc.put(key, body)
		return body, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// filterKey identifies one report window. Every field of ReportFilter that
// changes the result must appear here — a missed field would serve one
// tenant's numbers to another.
func filterKey(prefix string, f db.ReportFilter) string {
	return fmt.Sprintf("%s|w=%d|u=%d|from=%d|to=%d|tok=%d|model=%s|tag=%s",
		prefix, f.WorkspaceID, f.UserID, f.From.Unix(), f.To.Unix(), f.TokenID, f.Model, f.Tag)
}
