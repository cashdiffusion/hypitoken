// Package usage tracks per-auth token consumption and persists it to disk
// so stats survive restarts/upgrades. Updates are batched (5s ticker) and
// flushed atomically with fsync. Daily buckets give per-day totals.
package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// How many days of per-day history to keep. Older buckets are trimmed on
// each Record() so state.json stays bounded.
const dailyRetentionDays = 90

// How many hourly buckets to keep. Sized to comfortably cover the 5h
// rolling window used by the OAuth load balancer (plus headroom).
const hourlyRetentionHours = 24

// hourKeyFormat is the UTC layout used for Hourly bucket keys.
const hourKeyFormat = "2006-01-02T15"

// How many ISO weeks of per-client history to keep.
const weeklyRetentionWeeks = 26

type Counts struct {
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	CacheCreateTokens int64 `json:"cache_create_tokens"`
	CacheReadTokens   int64 `json:"cache_read_tokens"`
	Requests          int64 `json:"requests"`
	Errors            int64 `json:"errors"`
}

func (c *Counts) Add(o Counts) {
	c.InputTokens += o.InputTokens
	c.OutputTokens += o.OutputTokens
	c.CacheCreateTokens += o.CacheCreateTokens
	c.CacheReadTokens += o.CacheReadTokens
	c.Requests += o.Requests
	c.Errors += o.Errors
}

// WeightedTotal returns a cost-weighted token count used by the OAuth load
// balancer as its "how busy is this credential" metric. Weights roughly track
// Anthropic's pricing ratios so that cheap cache reads don't make a
// cache-heavy credential look overloaded, and output tokens (the scarce
// resource) dominate. Ratios: input=1, cache_create=1.25, cache_read=0.1,
// output=5. Multiplied by 100 and returned as int64 so the caller can keep
// integer comparisons (ties on identical load still break on auth ID).
func (c Counts) WeightedTotal() int64 {
	// scale = 100 → input weight becomes 100, cache_read becomes 10, etc.
	// Keeps everything in int64 without floats in the hot pick path.
	return c.InputTokens*100 + c.CacheCreateTokens*125 + c.CacheReadTokens*10 + c.OutputTokens*500
}

// DayEntry pairs a YYYY-MM-DD date with its counters for JSON rendering.
type DayEntry struct {
	Date   string `json:"date"`
	Counts Counts `json:"counts"`
}

// PerAuth tracks per-credential usage. Lifetime totals now come from the
// request log (see admin.handleSummary / requestlog.AggregateByAuth), so
// this struct only holds what we need on hot paths: Hourly buckets for the
// OAuth load balancer (see Sum5h), Daily buckets for the 14-day UI sparkline,
// plus LastUsed for the panel's "updated X ago" display.
type PerAuth struct {
	AuthID   string            `json:"auth_id"`
	Label    string            `json:"label,omitempty"`
	LastUsed time.Time         `json:"last_used,omitempty"`
	Daily    map[string]Counts `json:"daily,omitempty"`  // key = "YYYY-MM-DD" (UTC)
	Hourly   map[string]Counts `json:"hourly,omitempty"` // key = "YYYY-MM-DDTHH" (UTC)
}

// DailyOrdered returns the Daily map as a slice sorted by date ascending.
func (p *PerAuth) DailyOrdered(maxDays int) []DayEntry {
	if len(p.Daily) == 0 {
		return nil
	}
	keys := make([]string, 0, len(p.Daily))
	for k := range p.Daily {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if maxDays > 0 && len(keys) > maxDays {
		keys = keys[len(keys)-maxDays:]
	}
	out := make([]DayEntry, 0, len(keys))
	for _, k := range keys {
		out = append(out, DayEntry{Date: k, Counts: p.Daily[k]})
	}
	return out
}

// PerClient is per-access-token usage: tokens + USD per ISO week, plus
// lifetime total. Key in State.Clients is the access token string itself.
type PerClient struct {
	Token    string                `json:"token"`
	Label    string                `json:"label,omitempty"`
	Total    ClientCost            `json:"total"`
	Weekly   map[string]ClientCost `json:"weekly,omitempty"` // key = "2026-W15"
	LastUsed time.Time             `json:"last_used,omitempty"`
}

type ClientCost struct {
	Tokens   Counts  `json:"tokens"`
	CostUSD  float64 `json:"cost_usd"`
	Requests int64   `json:"requests"`
}

func (c *ClientCost) Add(o ClientCost) {
	c.Tokens.Add(o.Tokens)
	c.CostUSD += o.CostUSD
	c.Requests += o.Requests
}

// WeekEntry pairs ISO-week key with its cost for JSON rendering.
type WeekEntry struct {
	Week string     `json:"week"`
	Cost ClientCost `json:"cost"`
}

func (p *PerClient) WeeklyOrdered(maxWeeks int) []WeekEntry {
	if len(p.Weekly) == 0 {
		return nil
	}
	keys := make([]string, 0, len(p.Weekly))
	for k := range p.Weekly {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if maxWeeks > 0 && len(keys) > maxWeeks {
		keys = keys[len(keys)-maxWeeks:]
	}
	out := make([]WeekEntry, 0, len(keys))
	for _, k := range keys {
		out = append(out, WeekEntry{Week: k, Cost: p.Weekly[k]})
	}
	return out
}

type State struct {
	Auths   map[string]*PerAuth   `json:"auths"`
	Clients map[string]*PerClient `json:"clients,omitempty"`
}

type Store struct {
	mu       sync.Mutex
	state    *State
	path     string
	dirty    bool
	stopCh   chan struct{}
	doneCh   chan struct{}
	flushInt time.Duration
	now      func() time.Time // injectable clock (for tests)
}

// Open loads the state file (creating it if missing) and starts a background
// flusher. Close stops the flusher and performs one final fsynced flush.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Probe-write to detect path/permission misconfig early instead of letting
	// periodic flushes fail silently for the lifetime of the process. Mimics
	// the writeAtomic pattern (tmp file in the same dir, then unlink).
	probe := path + ".probe"
	if err := os.WriteFile(probe, []byte("ok"), 0600); err != nil {
		return nil, fmt.Errorf("state_file %s is not writable: %w", path, err)
	}
	_ = os.Remove(probe)
	s := &Store{
		state:    &State{Auths: make(map[string]*PerAuth)},
		path:     path,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		flushInt: 5 * time.Second,
		now:      time.Now,
	}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			log.Warnf("usage: parse %s: %v (starting empty)", path, err)
		} else {
			if st.Auths == nil {
				st.Auths = make(map[string]*PerAuth)
			}
			if st.Clients == nil {
				st.Clients = make(map[string]*PerClient)
			}
			s.state = &st
		}
	} else {
		s.state.Clients = make(map[string]*PerClient)
	}
	go s.loop()
	return s, nil
}

func (s *Store) Close() {
	select {
	case <-s.stopCh:
		// already closed
	default:
		close(s.stopCh)
	}
	<-s.doneCh
	_ = s.Flush()
}

func (s *Store) loop() {
	defer close(s.doneCh)
	t := time.NewTicker(s.flushInt)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			if err := s.Flush(); err != nil {
				log.Warnf("usage: periodic flush: %v", err)
			}
		}
	}
}

// Flush writes state atomically (tmp + rename + fsync) if dirty.
func (s *Store) Flush() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		s.mu.Lock()
		s.dirty = true
		s.mu.Unlock()
		return err
	}
	if werr := writeAtomic(s.path, data); werr != nil {
		s.mu.Lock()
		s.dirty = true
		s.mu.Unlock()
		return werr
	}
	return nil
}

// writeAtomic writes data via a tmp file + rename, then fsyncs the renamed
// file so it's durable across power loss (best-effort; filesystem dependent).
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// fsync the final file to flush metadata too.
	if final, err := os.OpenFile(path, os.O_RDONLY, 0); err == nil {
		_ = final.Sync()
		_ = final.Close()
	}
	return nil
}

// Record accumulates counts for an auth (both lifetime total and today's
// daily bucket) and marks dirty.
func (s *Store) Record(authID, label string, c Counts) {
	if authID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Auths[authID]
	if !ok {
		p = &PerAuth{AuthID: authID, Label: label, Daily: make(map[string]Counts)}
		s.state.Auths[authID] = p
	}
	if p.Daily == nil {
		p.Daily = make(map[string]Counts)
	}
	if p.Hourly == nil {
		p.Hourly = make(map[string]Counts)
	}
	if label != "" {
		p.Label = label
	}
	now := s.now()
	p.LastUsed = now
	utc := now.UTC()
	day := utc.Format("2006-01-02")
	cur := p.Daily[day]
	cur.Add(c)
	p.Daily[day] = cur
	hk := utc.Format(hourKeyFormat)
	hcur := p.Hourly[hk]
	hcur.Add(c)
	p.Hourly[hk] = hcur
	s.trimDailyLocked(p, now)
	s.trimHourlyLocked(p, now)
	s.dirty = true
}

func (s *Store) trimHourlyLocked(p *PerAuth, now time.Time) {
	if len(p.Hourly) <= hourlyRetentionHours {
		return
	}
	cutoff := now.UTC().Add(-time.Duration(hourlyRetentionHours) * time.Hour).Format(hourKeyFormat)
	for k := range p.Hourly {
		if k < cutoff {
			delete(p.Hourly, k)
		}
	}
}

func (s *Store) trimDailyLocked(p *PerAuth, now time.Time) {
	if len(p.Daily) <= dailyRetentionDays {
		return
	}
	cutoff := now.UTC().AddDate(0, 0, -dailyRetentionDays).Format("2006-01-02")
	for k := range p.Daily {
		if k < cutoff {
			delete(p.Daily, k)
		}
	}
}

// Snapshot returns a deep copy of current per-auth counts. Safe for JSON
// rendering by the admin handler.
func (s *Store) Snapshot() map[string]PerAuth {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]PerAuth, len(s.state.Auths))
	for k, v := range s.state.Auths {
		cp := *v
		if v.Daily != nil {
			cp.Daily = make(map[string]Counts, len(v.Daily))
			for dk, dv := range v.Daily {
				cp.Daily[dk] = dv
			}
		}
		if v.Hourly != nil {
			cp.Hourly = make(map[string]Counts, len(v.Hourly))
			for hk, hv := range v.Hourly {
				cp.Hourly[hk] = hv
			}
		}
		out[k] = cp
	}
	return out
}

// isoWeekKey returns "YYYY-Www" (ISO 8601) for the given time in UTC.
func isoWeekKey(t time.Time) string {
	y, w := t.UTC().ISOWeek()
	if w < 10 {
		return fmtWeek(y, w)
	}
	return fmtWeek(y, w)
}

func fmtWeek(y, w int) string {
	// sprintf-free tiny formatter to avoid a dependency just for this.
	ws := "0" + itoa(w)
	ws = ws[len(ws)-2:]
	return itoa(y) + "-W" + ws
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// RecordClient accumulates a client access token's usage + USD for the
// current ISO week. Call after a successful upstream response.
func (s *Store) RecordClient(token, label string, counts Counts, costUSD float64) {
	if token == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Clients == nil {
		s.state.Clients = make(map[string]*PerClient)
	}
	p, ok := s.state.Clients[token]
	if !ok {
		p = &PerClient{Token: token, Label: label, Weekly: make(map[string]ClientCost)}
		s.state.Clients[token] = p
	}
	if p.Weekly == nil {
		p.Weekly = make(map[string]ClientCost)
	}
	if label != "" {
		p.Label = label
	}
	now := s.now()
	add := ClientCost{Tokens: counts, CostUSD: costUSD, Requests: 1}
	p.Total.Add(add)
	p.LastUsed = now
	key := isoWeekKey(now)
	cur := p.Weekly[key]
	cur.Add(add)
	p.Weekly[key] = cur
	s.trimWeeklyLocked(p)
	s.dirty = true
}

func (s *Store) trimWeeklyLocked(p *PerClient) {
	if len(p.Weekly) <= weeklyRetentionWeeks {
		return
	}
	keys := make([]string, 0, len(p.Weekly))
	for k := range p.Weekly {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys[:len(keys)-weeklyRetentionWeeks] {
		delete(p.Weekly, k)
	}
}

// WeeklyCostUSD returns the current ISO-week USD spend for a client token.
// Zero if unknown.
func (s *Store) WeeklyCostUSD(token string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Clients == nil {
		return 0
	}
	p, ok := s.state.Clients[token]
	if !ok {
		return 0
	}
	key := isoWeekKey(s.now())
	return p.Weekly[key].CostUSD
}

// MergeClient folds src's per-client usage into dst and removes src.
// Weekly buckets are summed per key; totals accumulate; LastUsed takes
// the later of the two. dst.Label is preserved. No-op if src missing;
// errors if dst doesn't exist.
func (s *Store) MergeClient(src, dst string) error {
	if src == "" || dst == "" || src == dst {
		return fmt.Errorf("src and dst must differ and be non-empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Clients == nil {
		return fmt.Errorf("no client usage recorded")
	}
	srcP, ok := s.state.Clients[src]
	if !ok {
		return nil
	}
	dstP, ok := s.state.Clients[dst]
	if !ok {
		dstP = &PerClient{Token: dst, Weekly: make(map[string]ClientCost)}
		s.state.Clients[dst] = dstP
	}
	if dstP.Weekly == nil {
		dstP.Weekly = make(map[string]ClientCost)
	}
	for wk, cost := range srcP.Weekly {
		cur := dstP.Weekly[wk]
		cur.Add(cost)
		dstP.Weekly[wk] = cur
	}
	dstP.Total.Add(srcP.Total)
	if srcP.LastUsed.After(dstP.LastUsed) {
		dstP.LastUsed = srcP.LastUsed
	}
	delete(s.state.Clients, src)
	s.trimWeeklyLocked(dstP)
	s.dirty = true
	return nil
}

// RenameClient rekeys src's per-client record under dst. Refuses to
// clobber an existing dst entry — caller should merge instead. Silent
// no-op when src has no record yet (reset before first use).
func (s *Store) RenameClient(src, dst string) error {
	if src == "" || dst == "" || src == dst {
		return fmt.Errorf("src and dst must differ and be non-empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Clients == nil {
		return nil
	}
	p, ok := s.state.Clients[src]
	if !ok {
		return nil
	}
	if _, exists := s.state.Clients[dst]; exists {
		return fmt.Errorf("destination token already has usage recorded")
	}
	p.Token = dst
	s.state.Clients[dst] = p
	delete(s.state.Clients, src)
	s.dirty = true
	return nil
}

// SnapshotClients returns a deep copy of the clients map for JSON rendering.
func (s *Store) SnapshotClients() map[string]PerClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]PerClient, len(s.state.Clients))
	for k, v := range s.state.Clients {
		cp := *v
		if v.Weekly != nil {
			cp.Weekly = make(map[string]ClientCost, len(v.Weekly))
			for wk, wv := range v.Weekly {
				cp.Weekly[wk] = wv
			}
		}
		out[k] = cp
	}
	return out
}

// CurrentWeekKey exposes the ISO-week key for "now" (for callers that want
// to label UI cards).
func (s *Store) CurrentWeekKey() string {
	return isoWeekKey(s.now())
}

// Sum5h returns the total counts over the last ~5 hours (UTC), summing
// hourly buckets whose start hour falls within [now-5h, now]. This matches
// Anthropic's 5-hour rolling quota window closely enough to drive OAuth
// load balancing: the current partial hour plus up to five prior hours are
// included, so the returned window spans 5–6 hours depending on when in
// the hour the call happens.
func (s *Store) Sum5h(authID string) Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Auths[authID]
	if !ok || len(p.Hourly) == 0 {
		return Counts{}
	}
	now := s.now().UTC()
	cutoff := now.Add(-5 * time.Hour).Truncate(time.Hour).Format(hourKeyFormat)
	var sum Counts
	for k, v := range p.Hourly {
		if k >= cutoff {
			sum.Add(v)
		}
	}
	return sum
}

// Sum24h returns the total counts over the last 24 hours (UTC), using the
// last two daily buckets. This is approximate — it sums today + yesterday's
// buckets rather than a strict rolling window. Good enough for dashboards.
func (s *Store) Sum24h(authID string) Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Auths[authID]
	if !ok {
		return Counts{}
	}
	now := s.now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	var sum Counts
	sum.Add(p.Daily[today])
	sum.Add(p.Daily[yesterday])
	return sum
}
