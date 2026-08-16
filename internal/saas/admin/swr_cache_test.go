package admin

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A cold burst must collapse into one fetch (singleflight), and every caller
// must get that fetch's value.
func TestSWRCacheColdSingleflight(t *testing.T) {
	var c swrCache[int]
	var calls atomic.Int32
	fetch := func(context.Context) (int, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return 42, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := c.get(fetch)
			if err != nil || v != 42 {
				t.Errorf("get = %d, %v", v, err)
			}
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("cold burst ran %d fetches, want 1", n)
	}
}

// A stale entry is served immediately; the refresh happens off the caller's
// path and lands for later callers.
func TestSWRCacheStaleWhileRevalidate(t *testing.T) {
	var c swrCache[int]
	var calls atomic.Int32
	fetch := func(context.Context) (int, error) {
		return int(calls.Add(1)), nil
	}

	if v, _ := c.get(fetch); v != 1 {
		t.Fatalf("cold = %d, want 1", v)
	}
	c.mu.Lock()
	c.at = time.Now().Add(-swrCacheTTL - time.Second) // force staleness
	c.mu.Unlock()

	start := time.Now()
	v, err := c.get(fetch)
	if err != nil || v != 1 {
		t.Fatalf("stale get = %d, %v; want cached 1", v, err)
	}
	if d := time.Since(start); d > 20*time.Millisecond {
		t.Fatalf("stale get blocked %v", d)
	}

	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("background refresh never ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The refreshed value serves once stored.
	for time.Now().Before(deadline) {
		if v, _ := c.get(fetch); v == 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("refreshed value never served")
}
