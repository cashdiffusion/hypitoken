package admin

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/CPA-Claude/internal/requestlog"
	"github.com/wjsoj/CPA-Claude/internal/usage"
)

// RegisterStatus mounts the public /status/ SPA + API. Unlike Register(),
// this does not require admin_token — the page is intentionally anonymous.
// Per-token lookups require the full client token as proof of ownership;
// aggregate info is redacted (no emails, no full tokens, no file paths).
func (h *Handler) RegisterStatus(r *gin.Engine) {
	sub, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		log.Errorf("status: failed to scope embed FS: %v", err)
		return
	}
	r.GET("/status", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/status/")
	})
	r.GET("/status/", func(c *gin.Context) {
		serveAsset(c, sub, "index.html")
	})
	r.GET("/status/assets/*filepath", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("filepath"), "/")
		if p == "" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		serveAsset(c, sub, "assets/"+p)
	})
	r.GET("/status/api/overview", h.handleStatusOverview)
	r.GET("/status/api/dashboard", h.handleStatusDashboard)
	r.POST("/status/api/query", h.handleStatusQuery)
	r.POST("/status/api/history", h.handleStatusHistory)
	log.Info("admin: public /status/ page enabled")
}

// Pseudonyms used to anonymize client-token identities on the public
// dashboard. The pool is intentionally wider than the 26 cryptographic
// standbys to reduce collision rate for small- to mid-scale deployments.
var statusPseudonyms = []string{
	"Alice", "Bob", "Carol", "Dave", "Eve", "Frank", "Grace", "Heidi",
	"Ivan", "Judy", "Mallory", "Niaj", "Olivia", "Peggy", "Quentin", "Rupert",
	"Sybil", "Trent", "Uma", "Victor", "Walter", "Xena", "Yvonne", "Zach",
	"Aria", "Blake", "Cleo", "Dax", "Enzo", "Faye", "Gus", "Hana",
	"Iris", "Jace", "Kai", "Luna", "Milo", "Nova", "Otto", "Pia",
	"Quill", "Remy", "Sage", "Tess", "Ulli", "Vera", "Wren", "Yuri",
}

// pseudonymFor maps a stable identifier (masked client token, or a
// display name when the backend has already remapped) to a deterministic
// pseudonym. Collisions are possible for > ~50 distinct identities; we
// accept them — the goal is public obfuscation, not perfect pseudonymity.
// Implementation is FNV-1a 32-bit, inlined to avoid an import just for
// this one call site.
func pseudonymFor(key string) string {
	if key == "" {
		return "Anon"
	}
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return statusPseudonyms[int(h)%len(statusPseudonyms)]
}

// anonymizeByClient rebuilds the ByClient aggregate map with pseudonymous
// keys. Real display names/masks are never sent over the public wire.
// Collisions merge buckets (Count/Tokens/Cost summed) — acceptable because
// the dashboard shows totals, not identity.
func anonymizeByClient(in map[string]requestlog.Aggregate) map[string]requestlog.Aggregate {
	if len(in) == 0 {
		return in
	}
	out := make(map[string]requestlog.Aggregate, len(in))
	for k, v := range in {
		name := pseudonymFor(k)
		if existing, ok := out[name]; ok {
			existing.Count += v.Count
			existing.InputTokens += v.InputTokens
			existing.OutputTokens += v.OutputTokens
			existing.CacheReadTokens += v.CacheReadTokens
			existing.CacheCreateTokens += v.CacheCreateTokens
			existing.CostUSD += v.CostUSD
			existing.Errors += v.Errors
			existing.TotalDurationMs += v.TotalDurationMs
			out[name] = existing
		} else {
			out[name] = v
		}
	}
	return out
}

// ---- /status/api/dashboard ----

type statusDashboardPool struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	Quota     int `json:"quota"`
	Unhealthy int `json:"unhealthy"`
	Disabled  int `json:"disabled"`
	OAuth     int `json:"oauth"`
	APIKey    int `json:"apikey"`
}

type statusDashboardRequests struct {
	Summary  requestlog.Aggregate            `json:"summary"`
	ByClient map[string]requestlog.Aggregate `json:"by_client"`
	ByModel  map[string]requestlog.Aggregate `json:"by_model"`
	ByDay    map[string]requestlog.Aggregate `json:"by_day"`
}

type statusDashboard struct {
	Pool        statusDashboardPool     `json:"pool"`
	Pricing     any                     `json:"pricing,omitempty"`
	Requests14d statusDashboardRequests `json:"requests_14d"`
	RequestsAll statusDashboardRequests `json:"requests_all"`
	Hourly24h   []requestlog.HourBucket `json:"hourly_24h"`
}

func (h *Handler) handleStatusDashboard(c *gin.Context) {
	var out statusDashboard

	// Pool health — same counts the /overview endpoint produces, inlined
	// here so the SPA only needs one round trip for the dashboard tab.
	for _, st := range h.pool.Status() {
		out.Pool.Total++
		if st.Auth.Kind == auth.KindAPIKey {
			out.Pool.APIKey++
		} else {
			out.Pool.OAuth++
		}
		live := h.pool.FindByID(st.Auth.ID)
		var healthy, hardFail bool
		if live != nil {
			healthy, hardFail, _, _ = live.HealthSnapshot()
		}
		quota := !st.Auth.QuotaExceededAt.IsZero()
		switch {
		case st.Auth.Disabled:
			out.Pool.Disabled++
		case quota:
			out.Pool.Quota++
		case hardFail || !healthy:
			out.Pool.Unhealthy++
		default:
			out.Pool.Healthy++
		}
	}

	// Pricing (public — same shape admin /summary exposes).
	if h.pricing != nil {
		out.Pricing = gin.H{
			"default": h.pricing.Default(),
			"models":  h.pricing.Models(),
		}
	}

	if h.cfg.LogDir == "" {
		c.JSON(http.StatusOK, out)
		return
	}

	// 14-day window.
	today := time.Now().UTC()
	from14 := today.AddDate(0, 0, -13).Truncate(24 * time.Hour)
	to := today.Add(24 * time.Hour)
	if res, err := h.cachedQuery(requestlog.Filter{
		Dir: h.cfg.LogDir, From: from14, To: to, Limit: 1,
	}); err == nil {
		out.Requests14d = statusDashboardRequests{
			Summary:  res.Summary,
			ByClient: anonymizeByClient(res.ByClient),
			ByModel:  res.ByModel,
			ByDay:    res.ByDay,
		}
	}

	// All-time — needed for cache stats, tokens/$ and weekly/monthly charts.
	if res, err := h.cachedQuery(requestlog.Filter{
		Dir: h.cfg.LogDir, Limit: 1,
	}); err == nil {
		out.RequestsAll = statusDashboardRequests{
			Summary:  res.Summary,
			ByClient: anonymizeByClient(res.ByClient),
			ByModel:  res.ByModel,
			ByDay:    res.ByDay,
		}
	}

	// 24h hourly.
	if buckets, err := requestlog.AggregateHourly(h.cfg.LogDir, 24); err == nil {
		out.Hourly24h = buckets
	}

	c.JSON(http.StatusOK, out)
}

// ---- /status/api/overview ----

type statusOverviewAuth struct {
	Kind          string `json:"kind"`
	Label         string `json:"label,omitempty"`
	Group         string `json:"group,omitempty"`
	Healthy       bool   `json:"healthy"`
	Disabled      bool   `json:"disabled,omitempty"`
	QuotaExceeded bool   `json:"quota_exceeded,omitempty"`
	HardFailure   bool   `json:"hard_failure,omitempty"`
}

type statusOverview struct {
	Counts struct {
		Total     int `json:"total"`
		Healthy   int `json:"healthy"`
		Quota     int `json:"quota"`
		Unhealthy int `json:"unhealthy"`
		Disabled  int `json:"disabled"`
		OAuth     int `json:"oauth"`
		APIKey    int `json:"apikey"`
		Models    int `json:"models"`
	} `json:"counts"`
	Window struct {
		Requests int64   `json:"requests"`
		CostUSD  float64 `json:"cost_usd"`
		Errors   int64   `json:"errors"`
	} `json:"window_24h"`
	Auths []statusOverviewAuth `json:"auths"`
}

func (h *Handler) handleStatusOverview(c *gin.Context) {
	var out statusOverview
	out.Auths = []statusOverviewAuth{}
	for _, st := range h.pool.Status() {
		kind := "oauth"
		if st.Auth.Kind == auth.KindAPIKey {
			kind = "apikey"
			out.Counts.APIKey++
		} else {
			out.Counts.OAuth++
		}
		out.Counts.Total++

		live := h.pool.FindByID(st.Auth.ID)
		var healthy, hardFail bool
		if live != nil {
			healthy, hardFail, _, _ = live.HealthSnapshot()
		}
		quota := !st.Auth.QuotaExceededAt.IsZero()
		switch {
		case st.Auth.Disabled:
			out.Counts.Disabled++
		case quota:
			out.Counts.Quota++
		case hardFail || !healthy:
			out.Counts.Unhealthy++
		default:
			out.Counts.Healthy++
		}
		// Truncate label to 48 chars to keep the page defensive against
		// operators who stuff private info (e.g. email) into the label.
		label := st.Auth.Label
		if len(label) > 48 {
			label = label[:48] + "…"
		}
		out.Auths = append(out.Auths, statusOverviewAuth{
			Kind:          kind,
			Label:         label,
			Group:         st.Auth.Group,
			Healthy:       healthy && !quota && !hardFail && !st.Auth.Disabled,
			Disabled:      st.Auth.Disabled,
			QuotaExceeded: quota,
			HardFailure:   hardFail,
		})
	}
	out.Counts.Models = len(h.pricing.Models())

	// 24h aggregate across the whole pool.
	if h.cfg.LogDir != "" {
		agg, err := requestlog.AggregateByAuth(h.cfg.LogDir, time.Now().Add(-24*time.Hour), time.Time{})
		if err == nil {
			for _, a := range agg {
				out.Window.Requests += a.Count
				out.Window.CostUSD += a.CostUSD
				out.Window.Errors += a.Errors
			}
		}
	}
	c.JSON(http.StatusOK, out)
}

// ---- /status/api/query ----

type statusQueryBody struct {
	Tokens []string `json:"tokens"`
}

type statusTokenResult struct {
	Masked        string                `json:"masked"`
	Found         bool                  `json:"found"`
	Name          string                `json:"name,omitempty"`
	Group         string                `json:"group,omitempty"`
	WeeklyLimit   float64               `json:"weekly_limit"`
	WeeklyUsedUSD float64               `json:"weekly_used_usd"`
	Blocked       bool                  `json:"blocked"`
	Total         usage.ClientCost      `json:"total"`
	Weekly        []usage.WeekEntry     `json:"weekly,omitempty"`
	LastUsed      *time.Time            `json:"last_used,omitempty"`
	Recent        []statusRecentEntry   `json:"recent,omitempty"`
	RecentTotal   int                   `json:"recent_total,omitempty"`
	Daily         []statusDailyEntry    `json:"daily,omitempty"`
	Window24h     *requestlog.Aggregate `json:"window_24h,omitempty"`
}

type statusDailyEntry struct {
	Date     string  `json:"date"`
	CostUSD  float64 `json:"cost_usd"`
	Requests int64   `json:"requests"`
}

type statusRecentEntry struct {
	TS         time.Time `json:"ts"`
	Model      string    `json:"model,omitempty"`
	Input      int64     `json:"input_tokens"`
	Output     int64     `json:"output_tokens"`
	CacheRead  int64     `json:"cache_read_tokens"`
	CacheWrite int64     `json:"cache_create_tokens"`
	CostUSD    float64   `json:"cost_usd"`
	Status     int       `json:"status"`
	DurationMs int64     `json:"duration_ms"`
	Stream     bool      `json:"stream,omitempty"`
	AuthLabel  string    `json:"auth_label,omitempty"`
	AuthKind   string    `json:"auth_kind,omitempty"`
}

const statusRecentLimit = 60
const statusTokensPerRequest = 20
const statusDailyWindowDays = 14

func (h *Handler) handleStatusQuery(c *gin.Context) {
	var body statusQueryBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Dedupe and cap to prevent abuse of the log scan.
	seen := make(map[string]bool)
	tokens := make([]string, 0, len(body.Tokens))
	for _, t := range body.Tokens {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tokens = append(tokens, t)
		if len(tokens) >= statusTokensPerRequest {
			break
		}
	}

	// Pre-resolve each token: validate against the store, snapshot usage.
	// A token that isn't registered returns found=false with no data — we
	// don't reveal whether it was deleted vs never existed.
	clients := h.usage.SnapshotClients()
	currentWeek := h.usage.CurrentWeekKey()
	maskedIdx := make(map[string]int, len(tokens)) // masked → result index
	results := make([]statusTokenResult, 0, len(tokens))
	for _, tok := range tokens {
		masked := maskToken(tok)
		r := statusTokenResult{Masked: masked}
		name, weekly, _, group, ok := h.tokens.Lookup(tok)
		if !ok {
			results = append(results, r)
			continue
		}
		r.Found = true
		r.Name = name
		r.Group = group
		r.WeeklyLimit = weekly
		if pc, hasData := clients[tok]; hasData {
			r.Total = pc.Total
			r.Weekly = pc.WeeklyOrdered(8)
			if !pc.LastUsed.IsZero() {
				lu := pc.LastUsed
				r.LastUsed = &lu
			}
			if w, ok := pc.Weekly[currentWeek]; ok {
				r.WeeklyUsedUSD = w.CostUSD
			}
		}
		if r.WeeklyLimit > 0 && r.WeeklyUsedUSD >= r.WeeklyLimit {
			r.Blocked = true
		}
		// Use the masked form as the correlation key for the log scan — the
		// request log itself stores only masked tokens, so this comparison
		// is already what the writer will have emitted.
		maskedIdx[masked] = len(results)
		results = append(results, r)
	}

	// Log scan: walk the full archive once and bucket per token. The same
	// entries feed the Recent ledger (newest-first, first page of
	// statusRecentLimit), the 24h aggregate, the per-day cost/request
	// series for the last N days, and a recent_total count so the paging
	// UI knows how many total entries exist.
	if h.cfg.LogDir != "" && len(maskedIdx) > 0 {
		type bucket struct {
			agg         requestlog.Aggregate
			recent      []statusRecentEntry
			recentTotal int
			daily       map[string]*statusDailyEntry
		}
		seedDays := make([]string, 0, statusDailyWindowDays)
		today := time.Now().UTC()
		for i := statusDailyWindowDays - 1; i >= 0; i-- {
			d := today.AddDate(0, 0, -i)
			seedDays = append(seedDays, d.Format("2006-01-02"))
		}
		buckets := make(map[string]*bucket, len(maskedIdx))
		for k := range maskedIdx {
			b := &bucket{daily: make(map[string]*statusDailyEntry, statusDailyWindowDays)}
			for _, day := range seedDays {
				b.daily[day] = &statusDailyEntry{Date: day}
			}
			buckets[k] = b
		}
		cutoff24h := time.Now().Add(-24 * time.Hour)
		// Display-time remap: the log stores a snapshot of the auth label at
		// request time. When an auth is renamed, callers expect the UI to show
		// the current label (the audit trail is keyed by AuthID). Resolve once
		// per call and rewrite on emit; stale entries whose AuthID has been
		// deleted fall back to the snapshot value.
		labelIdx := h.pool.LabelIndex()
		// No From bound — scan the whole archive. Limit is a safety cap;
		// real deployments won't hit it.
		res, err := requestlog.Query(requestlog.Filter{
			Dir:   h.cfg.LogDir,
			Limit: 200000,
		})
		if err == nil {
			for _, rec := range res.Entries {
				b, ok := buckets[rec.ClientToken]
				if !ok {
					continue
				}
				b.recentTotal++
				if !rec.TS.Before(cutoff24h) {
					b.agg.Count++
					b.agg.InputTokens += rec.Input
					b.agg.OutputTokens += rec.Output
					b.agg.CacheReadTokens += rec.CacheRead
					b.agg.CacheCreateTokens += rec.CacheCreate
					b.agg.CostUSD += rec.CostUSD
					b.agg.TotalDurationMs += rec.DurationMs
					if rec.Status >= 400 || rec.Error != "" {
						b.agg.Errors++
					}
				}
				dayKey := rec.TS.UTC().Format("2006-01-02")
				if d, ok := b.daily[dayKey]; ok {
					d.CostUSD += rec.CostUSD
					d.Requests++
				}
				if len(b.recent) < statusRecentLimit {
					label, kind := rec.AuthLabel, rec.AuthKind
					if cur, ok := labelIdx[rec.AuthID]; ok {
						label = cur.Label
						kind = authKindString(cur.Kind)
					}
					b.recent = append(b.recent, statusRecentEntry{
						TS:         rec.TS,
						Model:      rec.Model,
						Input:      rec.Input,
						Output:     rec.Output,
						CacheRead:  rec.CacheRead,
						CacheWrite: rec.CacheCreate,
						CostUSD:    rec.CostUSD,
						Status:     rec.Status,
						DurationMs: rec.DurationMs,
						Stream:     rec.Stream,
						AuthLabel:  label,
						AuthKind:   kind,
					})
				}
			}
		}
		for m, b := range buckets {
			i := maskedIdx[m]
			if b.agg.Count > 0 {
				a := b.agg
				results[i].Window24h = &a
			}
			if len(b.recent) > 0 {
				results[i].Recent = b.recent
			}
			results[i].RecentTotal = b.recentTotal
			daily := make([]statusDailyEntry, 0, len(seedDays))
			for _, day := range seedDays {
				daily = append(daily, *b.daily[day])
			}
			results[i].Daily = daily
		}
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}

// ---- /status/api/history ----
//
// Paged ledger for a single client token over an arbitrary time range.
// Kept separate from /query so batch overview requests stay lean; this one
// scans the full log archive (all rotated files) once per invocation.

type statusHistoryBody struct {
	Token  string `json:"token"`
	From   string `json:"from,omitempty"`   // YYYY-MM-DD or RFC3339
	To     string `json:"to,omitempty"`     // YYYY-MM-DD or RFC3339
	Offset int    `json:"offset,omitempty"` // newest-first index
	Limit  int    `json:"limit,omitempty"`  // default 50, max 200
}

type statusHistoryResp struct {
	Entries []statusRecentEntry `json:"entries"`
	Total   int                 `json:"total"`
	Offset  int                 `json:"offset"`
	Limit   int                 `json:"limit"`
}

func (h *Handler) handleStatusHistory(c *gin.Context) {
	if h.cfg.LogDir == "" {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "log_dir not configured"})
		return
	}
	var body statusHistoryBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tok := strings.TrimSpace(body.Token)
	if tok == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "token required"})
		return
	}
	// Same ownership check as /query: the caller must present a token the
	// store knows about. We don't reveal whether an unknown token was once
	// valid or never existed.
	if _, _, _, _, ok := h.tokens.Lookup(tok); !ok {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "token not found"})
		return
	}
	masked := maskToken(tok)

	limit := body.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := body.Offset
	if offset < 0 {
		offset = 0
	}

	f := requestlog.Filter{Dir: h.cfg.LogDir, Limit: 100000}
	if body.From != "" {
		if t, err := parseDateBound(body.From, false); err == nil {
			f.From = t
		}
	}
	if body.To != "" {
		if t, err := parseDateBound(body.To, true); err == nil {
			f.To = t
		}
	}
	res, err := requestlog.Query(f)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Query already sorts res.Entries newest-first. Filter by masked token,
	// then paginate. Res.Entries may be truncated to f.Limit (100k) — which
	// is a guard for absurd archives; real deployments stay well under it.
	// Auth label/kind are remapped from current pool state so renames show
	// up in the ledger; snapshots survive as fallback for deleted auths.
	labelIdx := h.pool.LabelIndex()
	all := make([]statusRecentEntry, 0, 128)
	for _, rec := range res.Entries {
		if rec.ClientToken != masked {
			continue
		}
		label, kind := rec.AuthLabel, rec.AuthKind
		if cur, ok := labelIdx[rec.AuthID]; ok {
			label = cur.Label
			kind = authKindString(cur.Kind)
		}
		all = append(all, statusRecentEntry{
			TS:         rec.TS,
			Model:      rec.Model,
			Input:      rec.Input,
			Output:     rec.Output,
			CacheRead:  rec.CacheRead,
			CacheWrite: rec.CacheCreate,
			CostUSD:    rec.CostUSD,
			Status:     rec.Status,
			DurationMs: rec.DurationMs,
			Stream:     rec.Stream,
			AuthLabel:  label,
			AuthKind:   kind,
		})
	}
	total := len(all)
	if offset >= total {
		all = nil
	} else {
		all = all[offset:]
	}
	if len(all) > limit {
		all = all[:limit]
	}
	c.JSON(http.StatusOK, statusHistoryResp{
		Entries: all,
		Total:   total,
		Offset:  offset,
		Limit:   limit,
	})
}
