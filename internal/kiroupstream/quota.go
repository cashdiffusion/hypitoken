package kiroupstream

import (
	"context"
	"sync"
	"time"

	"github.com/wjsoj/cc-core/kiroapi"
	"github.com/wjsoj/cc-core/kirotransport"

	"github.com/wjsoj/CPA-Claude/internal/kirocreds"
)

// quotaTTL is how long a getUsageLimits result is cached per credential.
// Short enough that ramping back up after a reset is responsive, long
// enough that high-RPS endpoints don't fire one probe per request.
const quotaTTL = 60 * time.Second

// QuotaCache memoizes per-credential Kiro quota state. Callers ask
// allowed(entry) — true means the cached "remaining > 0" decision is
// still fresh; false means the credential is at zero quota AND the
// cache hasn't expired yet, so picker should skip it.
//
// On cache miss / stale entry, allowed() does a live getUsageLimits
// probe synchronously (with a short ctx). Probe failure is treated as
// allowed=true (fail-open) so a transient telemetry hiccup doesn't
// turn into a 503 cascade.
type QuotaCache struct {
	mu   sync.Mutex
	rows map[string]quotaRow // keyed by Entry.ID
}

type quotaRow struct {
	remaining float64
	at        time.Time
}

func newQuotaCache() *QuotaCache {
	return &QuotaCache{rows: make(map[string]quotaRow)}
}

// allowed returns true when the credential has spare quota OR the probe
// failed (fail-open). Returns false only when the cached probe explicitly
// said remaining <= 0 and the cache hasn't expired.
func (q *QuotaCache) allowed(ctx context.Context, e *kirocreds.Entry) bool {
	q.mu.Lock()
	row, ok := q.rows[e.ID]
	fresh := ok && time.Since(row.at) < quotaTTL
	q.mu.Unlock()
	if fresh {
		return row.remaining > 0
	}
	// Live probe with a tight deadline so the picker doesn't stall.
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := &kiroapi.Client{
		Token:    e.Cred.AccessToken,
		IsAPIKey: e.Cred.IsAPIKey(),
		Flavor:   kirotransport.FlavorCLI,
	}
	resp, err := client.GetCredits(pctx, e.Cred.ProfileARN)
	if err != nil {
		// Fail-open: don't penalize the credential for a probe glitch.
		return true
	}
	remaining := resp.Remaining()
	q.mu.Lock()
	q.rows[e.ID] = quotaRow{remaining: remaining, at: time.Now()}
	q.mu.Unlock()
	return remaining > 0
}

// Invalidate forces a fresh probe on the next allowed() call. Useful when
// the caller knows the cached value is stale (e.g. just observed a 429
// from upstream).
func (q *QuotaCache) Invalidate(id string) {
	q.mu.Lock()
	delete(q.rows, id)
	q.mu.Unlock()
}
