package requestlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Aggregate sums counters over a set of records.
type Aggregate struct {
	Count             int64   `json:"count"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CacheReadTokens   int64   `json:"cache_read_tokens"`
	CacheCreateTokens int64   `json:"cache_create_tokens"`
	CostUSD           float64 `json:"cost_usd"`
	Errors            int64   `json:"errors"`
	TotalDurationMs   int64   `json:"total_duration_ms"`
}

func (a *Aggregate) add(r Record) {
	a.Count++
	a.InputTokens += r.Input
	a.OutputTokens += r.Output
	a.CacheReadTokens += r.CacheRead
	a.CacheCreateTokens += r.CacheCreate
	a.CostUSD += r.CostUSD
	a.TotalDurationMs += r.DurationMs
	if r.Status >= 400 || r.Error != "" {
		a.Errors++
	}
}

// Filter selects records. Empty string fields and zero time fields mean
// "no constraint".
type Filter struct {
	Dir         string
	From        time.Time // inclusive; compared at day granularity (UTC)
	To          time.Time // inclusive
	ClientToken string    // exact match on Record.ClientToken (masked); preferred over Client
	Client      string    // exact match on Record.Client (fallback for orphans)
	Model       string    // exact match on Record.Model
	Provider    string    // exact match on Record.Provider ("anthropic" | "openai"). Legacy records (no provider field) match when Provider == "anthropic" so back-fill isn't needed.
	Status      int       // 0 = any; otherwise exact match
	AuthID      string    // exact match on Record.AuthID (credential ID)
	Limit       int       // page size for Entries (0 = 50)
	Offset      int       // number of newest-first records to skip before Limit
	// UserID limits results to records emitted by a specific SaaS user.
	// Used by the public-facing /app/logs page so a customer sees only
	// their own bill. Zero = no constraint (operator query).
	UserID int64
}

// Result is the Query return value.
type Result struct {
	Summary  Aggregate            `json:"summary"`
	ByClient map[string]Aggregate `json:"by_client"`
	ByModel  map[string]Aggregate `json:"by_model"`
	ByDay    map[string]Aggregate `json:"by_day"`
	Entries  []Record             `json:"entries"`
	Scanned  int64                `json:"scanned"`
}

// HourBucket holds one hour's worth of aggregated counters.
type HourBucket struct {
	Hour              time.Time `json:"hour"` // UTC, truncated to the hour
	Count             int64     `json:"count"`
	InputTokens       int64     `json:"input_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	CacheReadTokens   int64     `json:"cache_read_tokens"`
	CacheCreateTokens int64     `json:"cache_create_tokens"`
	CostUSD           float64   `json:"cost_usd"`
	Errors            int64     `json:"errors"`
}

// AggregateHourly scans the log files that could contain records in the
// last `hours` window and returns `hours` consecutive hour buckets ending
// at the current hour (inclusive). Missing hours are returned zero-filled
// so the UI can render a continuous timeseries without gap handling.
func AggregateHourly(dir string, hours int) ([]HourBucket, error) {
	if hours <= 0 {
		hours = 24
	}
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-time.Duration(hours-1) * time.Hour)
	// The window can straddle up to three local days (start-day, end-day, and
	// UTC day roll). We scan every file whose day string is within the
	// inclusive UTC date range, which is cheap — at most 2 files for 24h.
	files, err := listLogFiles(dir)
	if err != nil {
		return nil, err
	}
	buckets := make([]HourBucket, hours)
	for i := 0; i < hours; i++ {
		buckets[i].Hour = start.Add(time.Duration(i) * time.Hour)
	}
	for _, path := range files {
		day := extractDay(path)
		if day < start.Format("2006-01-02") || day > now.Format("2006-01-02") {
			continue
		}
		fh, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for sc.Scan() {
			var r Record
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				continue
			}
			if r.TS.Before(start) || r.TS.After(now.Add(time.Hour)) {
				continue
			}
			idx := int(r.TS.UTC().Truncate(time.Hour).Sub(start) / time.Hour)
			if idx < 0 || idx >= hours {
				continue
			}
			b := &buckets[idx]
			b.Count++
			b.InputTokens += r.Input
			b.OutputTokens += r.Output
			b.CacheReadTokens += r.CacheRead
			b.CacheCreateTokens += r.CacheCreate
			b.CostUSD += r.CostUSD
			if r.Status >= 400 || r.Error != "" {
				b.Errors++
			}
		}
		_ = fh.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return buckets, nil
}

// AggregateByAuth scans rotated log files whose day is within [from, to]
// (inclusive; zero values mean "unbounded"), filters records by exact
// timestamp, and returns per-AuthID aggregates. Intended for the admin
// summary to compute accurate lifetime and rolling-window totals directly
// from the request log, bypassing the in-memory counter (which resets on
// restart / state rebuild).
func AggregateByAuth(dir string, from, to time.Time) (map[string]Aggregate, error) {
	files, err := listLogFiles(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Aggregate)
	for _, path := range files {
		day := extractDay(path)
		if !dayInRange(day, from, to) {
			continue
		}
		fh, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for sc.Scan() {
			var r Record
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				continue
			}
			if !from.IsZero() && r.TS.Before(from) {
				continue
			}
			if !to.IsZero() && r.TS.After(to) {
				continue
			}
			if r.AuthID == "" {
				continue
			}
			agg := out[r.AuthID]
			agg.add(r)
			out[r.AuthID] = agg
		}
		_ = fh.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Query scans rotated log files in dir that intersect the [From, To]
// window, applies the filter, aggregates counts, and returns the page
// [Offset, Offset+Limit) of matching entries sorted newest-first.
// Summary.Count holds the full match count, so callers can render
// pagination controls without a second query.
func Query(f Filter) (*Result, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	files, err := listLogFiles(f.Dir)
	if err != nil {
		return nil, err
	}
	// Files are "requests-YYYY-MM-DD.jsonl"; sort descending (newest first).
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	res := &Result{
		ByClient: make(map[string]Aggregate),
		ByModel:  make(map[string]Aggregate),
		ByDay:    make(map[string]Aggregate),
	}
	for _, path := range files {
		day := extractDay(path)
		if !dayInRange(day, f.From, f.To) {
			continue
		}
		if err := scanFile(path, f, res); err != nil {
			return nil, err
		}
	}
	// scanFile collects every matching record. Sort descending by timestamp
	// then paginate.
	sort.Slice(res.Entries, func(i, j int) bool {
		return res.Entries[i].TS.After(res.Entries[j].TS)
	})
	if f.Offset >= len(res.Entries) {
		res.Entries = nil
	} else {
		res.Entries = res.Entries[f.Offset:]
	}
	if len(res.Entries) > f.Limit {
		res.Entries = res.Entries[:f.Limit]
	}
	return res, nil
}

func scanFile(path string, f Filter, res *Result) error {
	fh, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for sc.Scan() {
		res.Scanned++
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		if !matches(r, f) {
			continue
		}
		res.Summary.add(r)
		// Key by masked ClientToken so renames don't split buckets; caller
		// remaps keys to current display names before returning to the UI.
		ckey := r.ClientToken
		if ckey == "" {
			ckey = r.Client
		}
		by := res.ByClient[ckey]
		by.add(r)
		res.ByClient[ckey] = by
		bm := res.ByModel[r.Model]
		bm.add(r)
		res.ByModel[r.Model] = bm
		dayKey := r.TS.UTC().Format("2006-01-02")
		bd := res.ByDay[dayKey]
		bd.add(r)
		res.ByDay[dayKey] = bd
		// Collect every matching record so Query can produce an accurate
		// total count and paginate by Offset. Memory cost is O(match count);
		// if that ever becomes a problem we can switch to a windowed scan.
		res.Entries = append(res.Entries, r)
	}
	return sc.Err()
}

func matches(r Record, f Filter) bool {
	if f.UserID != 0 && r.UserID != f.UserID {
		return false
	}
	if f.ClientToken != "" {
		if r.ClientToken != f.ClientToken {
			return false
		}
	} else if f.Client != "" && !strings.EqualFold(r.Client, f.Client) {
		return false
	}
	if f.Model != "" && !strings.EqualFold(r.Model, f.Model) {
		return false
	}
	if f.Provider != "" {
		// Records written before the provider field existed will have
		// r.Provider == "". Treat them as anthropic so historical data is
		// still reachable without a back-fill migration.
		rp := strings.ToLower(r.Provider)
		if rp == "" {
			rp = "anthropic"
		}
		if !strings.EqualFold(rp, f.Provider) {
			return false
		}
	}
	if f.Status != 0 && r.Status != f.Status {
		return false
	}
	if f.AuthID != "" && r.AuthID != f.AuthID {
		return false
	}
	if !f.From.IsZero() && r.TS.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && r.TS.After(f.To) {
		return false
	}
	return true
}

func listLogFiles(dir string) ([]string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "requests-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out, nil
}

func extractDay(path string) string {
	base := filepath.Base(path)
	base = strings.TrimPrefix(base, "requests-")
	base = strings.TrimSuffix(base, ".jsonl")
	return base
}

func dayInRange(day string, from, to time.Time) bool {
	if !from.IsZero() && day < from.UTC().Format("2006-01-02") {
		return false
	}
	if !to.IsZero() && day > to.UTC().Format("2006-01-02") {
		return false
	}
	return true
}
