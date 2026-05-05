// Package billing wires the existing pricing.Catalog into per-user wallet
// charges. The provider rate (CNY-per-virtual-USD) and per-group multiplier
// live on the user's pricing_groups row; the live CNY/USD exchange rate is
// fetched from a free public API and cached for 1h.
package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const defaultExchangeURL = "https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies/usd.json"

// Rate is the cached USD->CNY conversion. Goroutine-safe.
type Rate struct {
	mu       sync.RWMutex
	cnyPerUSD float64
	asOf     time.Time

	url      string
	fallback float64
}

// NewRate returns a new rate cache. fallback is used if no fetch has succeeded yet.
func NewRate(url string, fallback float64) *Rate {
	if url == "" {
		url = defaultExchangeURL
	}
	if fallback <= 0 {
		fallback = 7.2
	}
	return &Rate{url: url, fallback: fallback, cnyPerUSD: fallback}
}

// CNYPerUSD returns the cached rate. Always nonzero (uses fallback if unset).
func (r *Rate) CNYPerUSD() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.cnyPerUSD > 0 {
		return r.cnyPerUSD
	}
	return r.fallback
}

// AsOf returns when the rate was last refreshed (zero if never).
func (r *Rate) AsOf() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.asOf
}

// Refresh fetches the latest rate and updates the cache. Errors are logged
// and swallowed so rate fetching is never on the request hot path.
func (r *Rate) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return err
	}
	cli := &http.Client{Timeout: 10 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errFetchStatus(resp.StatusCode)
	}
	// fawazahmed0 currency-api shape: {"date":"...","usd":{"cny":7.18,...}}
	var payload struct {
		USD map[string]float64 `json:"usd"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if v, ok := payload.USD["cny"]; ok && v > 0 {
		r.mu.Lock()
		r.cnyPerUSD = v
		r.asOf = time.Now()
		r.mu.Unlock()
		return nil
	}
	return errNoCNYField
}

// RunRefresher periodically refreshes the rate. Cancel ctx to stop.
func (r *Rate) RunRefresher(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 1 * time.Hour
	}
	if err := r.Refresh(ctx); err != nil {
		log.Warnf("exchange-rate initial fetch failed (using fallback %.4f): %v", r.fallback, err)
	} else {
		log.Infof("exchange-rate: 1 USD = %.4f CNY (cached for %s)", r.CNYPerUSD(), interval)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.Refresh(ctx); err != nil {
				log.Warnf("exchange-rate refresh failed: %v", err)
			}
		}
	}
}

type fetchErr int

func (e fetchErr) Error() string { return "fetch status " + http.StatusText(int(e)) }
func errFetchStatus(s int) error  { return fetchErr(s) }

var errNoCNYField = errFetchErr("missing usd.cny in response")

type errFetchErr string

func (e errFetchErr) Error() string { return string(e) }
