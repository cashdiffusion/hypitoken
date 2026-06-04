// Package admin exposes a small REST API + an embedded SPA for managing
// OAuth credentials at runtime. Protected by config.AdminToken.
package admin

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	// used in Anthropic usage proxy below

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/clienttoken"
	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/usage"
)

//go:generate bash -c "cd web && bun install --frozen-lockfile && bun run build"

// The SPA under web/ is built with Vite; the bundled output in web/dist is
// what actually ships in the binary. The directory is committed with a
// .gitkeep so this embed works even on a clean checkout where `make web`
// hasn't run yet — the panel will 404 until the build step populates
// web/dist.
//
//go:embed all:web/dist
var webFS embed.FS

// SaaSDistFS exposes the embedded web/dist tree to the SaaS adapter so it can
// serve the new SPA at the root path without re-embedding the same files.
func SaaSDistFS() (fs.FS, error) { return fs.Sub(webFS, "web/dist") }

type Handler struct {
	cfg     *config.Config
	pool    *auth.Pool
	usage   *usage.Store
	pricing *pricing.Catalog
	tokens  *clienttoken.Store

	// Cache for the full-history log scan backing lifetime totals.
	// Scanning every rotated file on each summary refresh would be
	// wasteful; a short TTL keeps the UI snappy without serving data
	// older than the refresh cadence.
	lifetimeMu    sync.Mutex
	lifetimeCache map[string]requestlog.Aggregate
	lifetimeAt    time.Time

	// Cache for /api/requests responses. The overview dashboard polls
	// every 10s and issues two filter shapes (14-day window + all-time);
	// without caching each poll re-scans every rotated log file. A short
	// TTL keeps the UI live while collapsing N concurrent pollers into
	// one disk pass. Invalidation is by TTL only — the log is append-only
	// so minor staleness is acceptable.
	reqCacheMu sync.Mutex
	reqCache   map[string]reqCacheEntry

	// kiro is the Kiro side-channel access plug. nil = Kiro disabled.
	kiro KiroAccess
	// apiGroup remembers the /api router group from Register so
	// SetKiroAccess can lazily attach kiro routes when main.go finishes
	// wiring after Server.New + Register completes.
	apiGroup *gin.RouterGroup
}

type reqCacheEntry struct {
	at     time.Time
	result *requestlog.Result
}

const lifetimeCacheTTL = 15 * time.Second
const requestsCacheTTL = 15 * time.Second
const requestsCacheMax = 16

func New(cfg *config.Config, pool *auth.Pool, store *usage.Store, cat *pricing.Catalog, tokens *clienttoken.Store) *Handler {
	return &Handler{cfg: cfg, pool: pool, usage: store, pricing: cat, tokens: tokens}
}

func (h *Handler) lifetimeByAuth() map[string]requestlog.Aggregate {
	h.lifetimeMu.Lock()
	defer h.lifetimeMu.Unlock()
	if h.lifetimeCache != nil && time.Since(h.lifetimeAt) < lifetimeCacheTTL {
		return h.lifetimeCache
	}
	m, err := requestlog.AggregateByAuth(h.cfg.LogDir, time.Time{}, time.Time{})
	if err != nil {
		log.Warnf("admin: lifetime aggregate: %v", err)
		if h.lifetimeCache != nil {
			return h.lifetimeCache
		}
		return map[string]requestlog.Aggregate{}
	}
	h.lifetimeCache = m
	h.lifetimeAt = time.Now()
	return m
}

func aggToCounts(a requestlog.Aggregate) usage.Counts {
	return usage.Counts{
		InputTokens:       a.InputTokens,
		OutputTokens:      a.OutputTokens,
		CacheCreateTokens: a.CacheCreateTokens,
		CacheReadTokens:   a.CacheReadTokens,
		Requests:          a.Count,
		Errors:            a.Errors,
	}
}

// Register attaches the legacy admin JSON API at /admin/api/*. The SPA
// shell that used to live at a separate /mgmt-console/ path has been removed — the SaaS
// frontend (mounted at /) is the only operator UI now.
//
// If cfg.AdminToken is empty the API is disabled.
func (h *Handler) Register(r *gin.Engine) {
	if strings.TrimSpace(h.cfg.AdminToken) == "" {
		log.Info("admin: disabled (admin_token not set)")
		return
	}
	log.Infof("admin: legacy JSON API enabled at /admin/api/")

	api := r.Group("/admin/api")
	api.Use(h.adminAuth())
	h.apiGroup = api
	{
		api.GET("/summary", h.handleSummary)
		api.POST("/auths/upload", h.handleUpload)
		api.PATCH("/auths/:id", h.handlePatchAuth)
		api.DELETE("/auths/:id", h.handleDeleteAuth)
		api.POST("/auths/:id/refresh", h.handleRefresh)
		api.POST("/auths/:id/clear-quota", h.handleClearQuota)
		api.POST("/auths/:id/clear-failure", h.handleClearFailure)
		api.POST("/oauth/start", h.handleOAuthStart)
		api.POST("/oauth/finish", h.handleOAuthFinish)
		api.POST("/apikeys", h.handleCreateAPIKey)
		api.POST("/auths/:id/anthropic-usage", h.handleAnthropicUsage)
		api.POST("/auths/:id/codex-usage", h.handleCodexUsage)
		api.GET("/requests", h.handleRequestsQuery)
		api.GET("/requests/clients", h.handleRequestsClients)
		api.GET("/requests/hourly", h.handleRequestsHourly)
		api.GET("/tokens", h.handleListTokens)
		api.POST("/tokens", h.handleCreateToken)
		api.GET("/orphan-tokens", h.handleListOrphanTokens)
		api.PATCH("/tokens/:token", h.handlePatchToken)
		api.DELETE("/tokens/:token", h.handleDeleteToken)
		api.POST("/tokens/:token/reset", h.handleResetToken)
		api.POST("/tokens/:token/inherit", h.handleInheritToken)
		// Kiro side-channel endpoints (no-op when SetKiroAccess wasn't
		// called yet — late wiring re-attaches them).
		h.RegisterKiro(api)
	}
}

func serveAsset(c *gin.Context, root fs.FS, name string) {
	f, err := root.Open(name)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, guessMime(name), data)
}

func guessMime(name string) string {
	switch filepath.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".map":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

// SSOAuth is an optional hook that lets the SaaS layer turn a SaaS JWT
// into a legacy /admin/api/* authorization. main.go wires it up after the
// SaaS issuer is constructed; nil = SSO disabled (legacy token only).
//
// Returns (allowed, isAdmin):
//   - allowed=true means the caller is a logged-in SaaS user. The legacy
//     admin gate accepts the request.
//   - isAdmin reflects the user's SaaS role. For non-admins, mutation
//     endpoints (POST/PATCH/DELETE) are still blocked so a regular user
//     can read fleet stats but can't, say, delete an upstream credential.
//
// The hook is consulted only when the bearer token doesn't match the
// configured AdminToken — so legacy clients with the operator password
// keep working unchanged.
var SSOAuth func(c *gin.Context) (allowed bool, isAdmin bool)

// adminAuth verifies the X-Admin-Token header (or Bearer token) against
// config.AdminToken using constant-time compare. If neither matches and
// SSOAuth is registered, it falls back to SaaS JWT verification — that
// path lets a logged-in SaaS user reach the legacy console without ever
// learning the operator password.
func (h *Handler) adminAuth() gin.HandlerFunc {
	want := []byte(strings.TrimSpace(h.cfg.AdminToken))
	return func(c *gin.Context) {
		got := strings.TrimSpace(c.GetHeader("X-Admin-Token"))
		if got == "" {
			v := strings.TrimSpace(c.GetHeader("Authorization"))
			if strings.HasPrefix(strings.ToLower(v), "bearer ") {
				got = strings.TrimSpace(v[len("bearer "):])
			}
		}
		// Path 1: legacy operator token. Constant-time compare so a token
		// that happens to look like a SaaS JWT can't time-leak the secret.
		if got != "" && subtle.ConstantTimeCompare([]byte(got), want) == 1 {
			c.Next()
			return
		}
		// Path 2: SSO via SaaS JWT. Read-only endpoints are open to any
		// authenticated user (including non-admin SaaS users) — that's
		// the whole point: Overview/charts visible to anyone signed in.
		// Mutations require role=admin.
		if SSOAuth != nil {
			if allowed, isAdmin := SSOAuth(c); allowed {
				if isAdmin || c.Request.Method == http.MethodGet {
					c.Next()
					return
				}
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "operator role required"})
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid admin token"})
	}
}

// ---- responses ----

type authRow struct {
	ID            string        `json:"id"`
	Kind          string        `json:"kind"`
	Provider      string        `json:"provider"` // "anthropic" | "openai"
	PlanType      string        `json:"plan_type,omitempty"`
	Label         string        `json:"label"`
	Email         string        `json:"email,omitempty"`
	ProxyURL      string        `json:"proxy_url"`
	BaseURL       string        `json:"base_url,omitempty"`
	Group         string        `json:"group,omitempty"`
	MaxConcurrent int           `json:"max_concurrent"`
	ActiveClients int           `json:"active_clients"`
	ClientTokens  []string      `json:"client_tokens"`
	Disabled      bool          `json:"disabled"`
	QuotaExceeded bool          `json:"quota_exceeded"`
	QuotaResetAt  *time.Time    `json:"quota_reset_at,omitempty"`
	ExpiresAt     *time.Time    `json:"expires_at,omitempty"`
	LastFailure   string        `json:"last_failure,omitempty"`
	FileBacked    bool          `json:"file_backed"`
	Healthy       bool          `json:"healthy"`
	HardFailure   bool          `json:"hard_failure"`
	FailureReason string        `json:"failure_reason,omitempty"`
	// RefreshSuspended is true when the background OAuth refresher
	// (cc-core/auth Pool.RefreshExpiring) will deliberately skip this
	// credential — i.e. it is disabled or hard-failed. The frontend uses
	// this to explain why an OAuth token can show "expired Xd ago"
	// without ever being refreshed: refresh is intentionally frozen
	// pending operator action (clear-failure / re-enable).
	RefreshSuspended       bool   `json:"refresh_suspended,omitempty"`
	RefreshSuspendedReason string `json:"refresh_suspended_reason,omitempty"`
	// Most recent client-initiated cancellation. Informational only —
	// doesn't affect Healthy or trigger any cooldown. Used by the panel to
	// show a low-tone "client canceled" hint distinct from upstream
	// failures.
	LastClientCancel   *time.Time        `json:"last_client_cancel,omitempty"`
	ClientCancelReason string            `json:"client_cancel_reason,omitempty"`
	ModelMap           map[string]string `json:"model_map,omitempty"`
	Usage              *usageSummary     `json:"usage,omitempty"`
	// CodexRateLimits holds the latest x-codex-* response headers from
	// chatgpt.com — primary/secondary window used-percent, resets etc.
	// Empty for non-OAuth / non-Codex credentials or until first call.
	CodexRateLimits   map[string]string `json:"codex_rate_limits,omitempty"`
	CodexRateLimitsAt *time.Time        `json:"codex_rate_limits_at,omitempty"`
	// CodexUsage is the latest wham/usage snapshot (active probe of the
	// chatgpt.com web portal). Stays nil for non-Codex creds and for Codex
	// creds that have never been probed.
	CodexUsage   *auth.CodexUsageInfo `json:"codex_usage,omitempty"`
	CodexUsageAt *time.Time           `json:"codex_usage_at,omitempty"`
}

type usageSummary struct {
	Total  usage.Counts `json:"total"`
	Sum24h usage.Counts `json:"sum_24h"`
	// Sum5h is the in-memory rolling 5h window — used by the UI to show
	// "recent burn" for Codex OAuth creds (ChatGPT backend doesn't expose
	// a proactive remaining-quota API, so this is the best signal we have
	// before a 429 actually fires).
	Sum5h    usage.Counts     `json:"sum_5h"`
	LastUsed *time.Time       `json:"last_used,omitempty"`
	Daily    []usage.DayEntry `json:"daily"` // last 14 days, oldest first
	// TotalCostUSD is the lifetime USD spend routed through this credential,
	// summed from the request log. Includes advisor sub-call rows (priced
	// under their own model), so a credential's bar reflects true cost.
	TotalCostUSD float64 `json:"total_cost_usd"`
}

// buildAuthRows produces the rich credential rows used by both the legacy
// /summary endpoint and the SaaS /api/v2/admin/credentials bridge. Factored
// out so the SaaS panel can render the same data (usage / quota / model_map
// / client_tokens / codex rate-limits) without re-shipping all of /summary.
func (h *Handler) buildAuthRows() []authRow {
	usageMap := h.usage.Snapshot()
	lifetime := h.lifetimeByAuth()
	last24h, err := requestlog.AggregateByAuth(h.cfg.LogDir, time.Now().Add(-24*time.Hour), time.Time{})
	if err != nil {
		log.Warnf("admin: 24h aggregate: %v", err)
		last24h = map[string]requestlog.Aggregate{}
	}
	rows := make([]authRow, 0, 16)
	for _, st := range h.pool.Status() {
		kind := "oauth"
		if st.Auth.Kind == auth.KindAPIKey {
			kind = "apikey"
		}
		var quotaReset *time.Time
		if !st.Auth.QuotaResetAt.IsZero() {
			t := st.Auth.QuotaResetAt
			quotaReset = &t
		}
		var expAt *time.Time
		if !st.Auth.ExpiresAt.IsZero() {
			t := st.Auth.ExpiresAt
			expAt = &t
		}
		var u *usageSummary
		// Show a usage row for every auth that has either in-memory daily
		// history or any log-recorded activity, so lifetime totals keep
		// rendering even after a state rebuild wipes the in-memory store.
		v, hasMem := usageMap[st.Auth.ID]
		lifeAgg, hasLife := lifetime[st.Auth.ID]
		last24Agg := last24h[st.Auth.ID]
		if hasMem || hasLife {
			var lastPtr *time.Time
			if hasMem && !v.LastUsed.IsZero() {
				lu := v.LastUsed
				lastPtr = &lu
			}
			var daily []usage.DayEntry
			if hasMem {
				daily = v.DailyOrdered(14)
			}
			u = &usageSummary{
				Total:        aggToCounts(lifeAgg),
				Sum24h:       aggToCounts(last24Agg),
				Sum5h:        h.usage.Sum5h(st.Auth.ID),
				LastUsed:     lastPtr,
				Daily:        daily,
				TotalCostUSD: lifeAgg.CostUSD,
			}
		}
		live := h.pool.FindByID(st.Auth.ID)
		var healthy, hardFail bool
		var failReason string
		var cancelAt *time.Time
		var cancelReason string
		if live != nil {
			healthy, hardFail, failReason, _ = live.HealthSnapshot()
			if at, reason := live.ClientCancelSnapshot(); !at.IsZero() {
				t := at
				cancelAt = &t
				cancelReason = reason
			}
		}
		provider := auth.NormalizeProvider(st.Auth.Provider)
		var planType string
		if live != nil {
			_, planType = live.CodexIdentity()
		}
		// refresh_suspended mirrors the gate in cc-core/auth Pool.RefreshExpiring:
		// background refresh skips disabled and hard-failed credentials. We
		// surface the gate here so the admin UI can explain why an OAuth
		// shows "expired Xd ago" yet never gets refreshed — operator action
		// (re-enable or clear-failure) is required first.
		var refreshSuspended bool
		var refreshSuspendedReason string
		if st.Auth.Kind == auth.KindOAuth {
			if st.Auth.Disabled {
				refreshSuspended = true
				refreshSuspendedReason = "credential disabled"
			} else if hardFail {
				refreshSuspended = true
				if failReason != "" {
					refreshSuspendedReason = "hard failure: " + failReason
				} else {
					refreshSuspendedReason = "hard failure"
				}
			}
		}
		rows = append(rows, authRow{
			ID:            st.Auth.ID,
			Kind:          kind,
			Provider:      provider,
			PlanType:      planType,
			Label:         st.Auth.Label,
			Email:         st.Auth.Email,
			ProxyURL:      st.Auth.ProxyURL,
			BaseURL:       st.Auth.BaseURL,
			Group:         st.Auth.Group,
			MaxConcurrent: st.Auth.MaxConcurrent,
			ActiveClients: st.ActiveClients,
			ClientTokens:  h.resolveClientTokenLabels(st.ClientTokens),
			Disabled:      st.Auth.Disabled,
			QuotaExceeded: !st.Auth.QuotaExceededAt.IsZero(),
			QuotaResetAt:  quotaReset,
			ExpiresAt:     expAt,
			FileBacked:    strings.TrimSpace(st.Auth.FilePath) != "",
			Healthy:                healthy,
			HardFailure:            hardFail,
			FailureReason:          failReason,
			RefreshSuspended:       refreshSuspended,
			RefreshSuspendedReason: refreshSuspendedReason,
			LastClientCancel:   cancelAt,
			ClientCancelReason: cancelReason,
			ModelMap:           st.Auth.ModelMap,
			Usage:              u,
			CodexRateLimits: func() map[string]string {
				if live == nil {
					return nil
				}
				snap := live.Snapshot()
				return snap.CodexRateLimits
			}(),
			CodexRateLimitsAt: func() *time.Time {
				if live == nil {
					return nil
				}
				snap := live.Snapshot()
				if snap.CodexRateLimitsAt.IsZero() {
					return nil
				}
				t := snap.CodexRateLimitsAt
				return &t
			}(),
			CodexUsage: func() *auth.CodexUsageInfo {
				if live == nil {
					return nil
				}
				return live.Snapshot().CodexUsage
			}(),
			CodexUsageAt: func() *time.Time {
				if live == nil {
					return nil
				}
				snap := live.Snapshot()
				if snap.CodexUsageAt.IsZero() {
					return nil
				}
				t := snap.CodexUsageAt
				return &t
			}(),
		})
	}
	return rows
}

func (h *Handler) handleSummary(c *gin.Context) {
	rows := h.buildAuthRows()
	// Clients (per-access-token spending).
	clientSnap := h.usage.SnapshotClients()
	currentWeek := h.usage.CurrentWeekKey()
	clientRows := make([]clientRow, 0)
	seen := make(map[string]bool)
	addRow := func(token, label, group string, weeklyLimit float64, rpm int, fromConfig, managed bool) {
		seen[token] = true
		pc, hasData := clientSnap[token]
		weekly := 0.0
		var weeks []usage.WeekEntry
		var total usage.ClientCost
		var last *time.Time
		if hasData {
			weeks = pc.WeeklyOrdered(8)
			if w, ok := pc.Weekly[currentWeek]; ok {
				weekly = w.CostUSD
			}
			total = pc.Total
			if !pc.LastUsed.IsZero() {
				lu := pc.LastUsed
				last = &lu
			}
		}
		row := clientRow{
			Token:       maskToken(token),
			Label:       label,
			WeeklyUSD:   weekly,
			WeeklyLimit: weeklyLimit,
			Blocked:     weeklyLimit > 0 && weekly >= weeklyLimit,
			FromConfig:  fromConfig,
			Managed:     managed,
			Group:       group,
			RPM:         rpm,
			Total:       total,
			Weekly:      weeks,
			LastUsed:    last,
		}
		if managed || fromConfig {
			row.FullToken = token
		}
		clientRows = append(clientRows, row)
	}
	// Rows for every configured or runtime-added access token.
	for _, t := range h.tokens.List() {
		addRow(t.Token, t.Name, t.Group, t.WeeklyUSD, t.RPM, false, true)
	}
	// Rows for every client we've actually seen that isn't already listed
	// (e.g. open-mode requests keyed by IP).
	for tok, pc := range clientSnap {
		if !seen[tok] {
			addRow(tok, pc.Label, "", 0, 0, false, false)
		}
	}

	// Pricing view (editable in config.yaml, read-only here).
	priceView := make(map[string]pricing.ModelPrice)
	for k, v := range h.pricing.Models() {
		priceView[k] = v
	}
	c.JSON(http.StatusOK, gin.H{
		"active_window_minutes": h.cfg.ActiveWindowMinutes,
		"auth_dir":              h.cfg.AuthDir,
		"default_proxy_url":     h.cfg.DefaultProxyURL,
		"auths":                 rows,
		"clients":               clientRows,
		"current_week":          currentWeek,
		"pricing": gin.H{
			"default":           h.pricing.Default(),
			"provider_defaults": h.pricing.ProviderDefaults(),
			"models":            priceView,
		},
	})
}

type clientRow struct {
	// Masked token (e.g. "sk-tes…aaaa") for display.
	Token string `json:"token"`
	// Full token; only set for rows that correspond to a registered client
	// token (not for the synthetic IP-keyed rows in open mode). The panel
	// needs this to build PATCH/DELETE URLs — admin auth covers exposure.
	FullToken   string            `json:"full_token,omitempty"`
	Label       string            `json:"label,omitempty"`
	WeeklyUSD   float64           `json:"weekly_usd"`
	WeeklyLimit float64           `json:"weekly_limit"`
	Blocked     bool              `json:"blocked"`
	FromConfig  bool              `json:"from_config,omitempty"`
	Managed     bool              `json:"managed,omitempty"` // true = panel can edit/delete
	Group       string            `json:"group,omitempty"`
	// RPM is the per-token requests-per-minute override. 0 = use global default.
	RPM         int               `json:"rpm,omitempty"`
	Total       usage.ClientCost  `json:"total"`
	Weekly      []usage.WeekEntry `json:"weekly,omitempty"`
	LastUsed    *time.Time        `json:"last_used,omitempty"`
}

func maskToken(t string) string {
	if len(t) <= 10 {
		return "***"
	}
	return t[:6] + "…" + t[len(t)-4:]
}

// authKindString returns the wire-format tag ("oauth" / "apikey") used in the
// request log and admin API. Matches the string literal the proxy writes at
// request time so display-remapped entries stay schema-compatible.
func authKindString(k auth.Kind) string {
	if k == auth.KindAPIKey {
		return "apikey"
	}
	return "oauth"
}

// resolveClientTokenLabels turns raw client tokens into display strings for
// the admin panel. Registered tokens are shown by human name; unknown ones
// (open-mode IPs, stale entries) fall back to a masked form. Duplicates are
// collapsed with an "×N" suffix so N concurrent requests from the same client
// render as one tooltip entry.
func (h *Handler) resolveClientTokenLabels(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	order := make([]string, 0, len(tokens))
	counts := make(map[string]int, len(tokens))
	for _, t := range tokens {
		label := ""
		if h.tokens != nil {
			if entry, ok := h.tokens.Lookup(t); ok && strings.TrimSpace(entry.Name) != "" {
				label = entry.Name
			}
		}
		if label == "" {
			label = maskToken(t)
		}
		if _, ok := counts[label]; !ok {
			order = append(order, label)
		}
		counts[label]++
	}
	out := make([]string, 0, len(order))
	for _, label := range order {
		if counts[label] > 1 {
			out = append(out, fmt.Sprintf("%s ×%d", label, counts[label]))
		} else {
			out = append(out, label)
		}
	}
	return out
}

type patchAuthBody struct {
	Disabled      *bool              `json:"disabled"`
	MaxConcurrent *int               `json:"max_concurrent"`
	ProxyURL      *string            `json:"proxy_url"`
	BaseURL       *string            `json:"base_url"`
	Label         *string            `json:"label"`
	Group         *string            `json:"group"`
	ModelMap      *map[string]string `json:"model_map"`
}

func (h *Handler) handlePatchAuth(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.FindByID(id)
	if a == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	// API keys declared in config.yaml have no backing file — they can't be
	// edited at runtime. File-backed keys (in auth_dir) are mutable like OAuth.
	if a.Kind == auth.KindAPIKey && a.FilePath == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "config.yaml-defined API keys are read-only; edit the YAML and restart, or add the key via the panel instead"})
		return
	}
	var body patchAuthBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Disabled != nil {
		a.SetDisabled(*body.Disabled)
	}
	if body.MaxConcurrent != nil {
		a.SetMaxConcurrent(*body.MaxConcurrent)
	}
	if body.ProxyURL != nil {
		a.SetProxyURL(strings.TrimSpace(*body.ProxyURL))
	}
	if body.BaseURL != nil {
		a.SetBaseURL(strings.TrimRight(strings.TrimSpace(*body.BaseURL), "/"))
	}
	if body.Label != nil {
		label := strings.TrimSpace(*body.Label)
		if label != "" {
			a.Label = label
		}
	}
	if body.Group != nil {
		a.SetGroup(*body.Group)
	}
	if body.ModelMap != nil {
		// Rewrite table for both kinds. API-key relays remap vendor model names;
		// Claude OAuth uses it for the default opus-4-6/4-7 → 4-8 upgrade (and any
		// operator overrides). Applied at routing time via ResolveUpstreamModel.
		a.SetModelMap(*body.ModelMap)
	}
	if err := a.Persist(); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "persist failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) handleDeleteAuth(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.RemoveAuth(id)
	if a == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	if a.FilePath != "" {
		if err := os.Remove(a.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warnf("admin: failed to remove %s: %v", a.FilePath, err)
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) handleRefresh(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.FindByID(id)
	if a == nil || a.Kind != auth.KindOAuth {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "oauth not found"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	// EnsureFresh with a huge leeway forces refresh regardless of current expiry.
	if err := a.EnsureFresh(ctx, 365*24*time.Hour, h.pool.UseUTLS()); err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "expires_at": a.Snapshot().ExpiresAt})
}

func (h *Handler) handleClearQuota(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.FindByID(id)
	if a == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	a.ClearQuota()
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) handleClearFailure(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.FindByID(id)
	if a == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	a.ClearFailure()
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

type uploadBody struct {
	// Provider scopes the upload to the tab the operator was on when they
	// opened the modal. When the parsed file omits a `provider` field (or
	// a recognizable `type`), this fills it in. When both are present and
	// disagree, the request is rejected — we don't want a Claude OAuth
	// silently persisted into the Codex tab just because the file didn't
	// declare itself.
	Provider      string          `json:"provider"`
	Filename      string          `json:"filename"`
	Content       json.RawMessage `json:"content"`
	Label         string          `json:"label"`
	MaxConcurrent int             `json:"max_concurrent"`
	ProxyURL      string          `json:"proxy_url"`
	Disabled      bool            `json:"disabled"`
	Group         string          `json:"group"`
}

func (h *Handler) handleUpload(c *gin.Context) {
	var body uploadBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(body.Content) == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing content"})
		return
	}
	// Merge user-supplied metadata into the raw JSON so parseFile sees it.
	var merged map[string]any
	if err := json.Unmarshal(body.Content, &merged); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
		return
	}
	if merged == nil {
		merged = make(map[string]any)
	}
	// Reconcile the operator's tab-scoped provider choice with whatever the
	// uploaded JSON declares. Four cases:
	//   1. body.Provider empty, merged has no provider  → fall through, let
	//      parseFile infer from `type` (defaults to anthropic).
	//   2. body.Provider empty, merged has provider     → respect the file.
	//   3. body.Provider set, merged has no provider    → stamp it in.
	//   4. both set but mismatch                        → reject loudly.
	wantProv := auth.NormalizeProvider(body.Provider)
	if body.Provider != "" {
		if existing, _ := merged["provider"].(string); existing != "" {
			if auth.NormalizeProvider(existing) != wantProv {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
					"error": fmt.Sprintf("uploaded file declares provider=%q but the %s tab was active — open the matching tab and try again", existing, wantProv),
				})
				return
			}
		} else {
			merged["provider"] = wantProv
		}
	}
	if body.Label != "" {
		merged["label"] = body.Label
	}
	if body.MaxConcurrent > 0 {
		merged["max_concurrent"] = body.MaxConcurrent
	}
	if strings.TrimSpace(body.ProxyURL) != "" {
		merged["proxy_url"] = strings.TrimSpace(body.ProxyURL)
	}
	if body.Disabled {
		merged["disabled"] = true
	}
	if g := auth.NormalizeGroup(body.Group); g != "" {
		merged["group"] = g
	}

	// Derive target filename. Prefix with provider so the auths/ directory
	// is self-documenting when inspected directly on disk.
	finalProv, _ := merged["provider"].(string)
	finalProv = auth.NormalizeProvider(finalProv)
	prefix := "claude"
	if finalProv == auth.ProviderOpenAI {
		prefix = "codex"
	}
	name := sanitizeFilename(body.Filename)
	if name == "" {
		email, _ := merged["email"].(string)
		if email != "" {
			name = prefix + "-" + sanitizeFilename(email) + ".json"
		} else {
			name = fmt.Sprintf("%s-%d.json", prefix, time.Now().Unix())
		}
	}
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		name += ".json"
	}
	if err := os.MkdirAll(h.cfg.AuthDir, 0700); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	full := filepath.Join(h.cfg.AuthDir, name)

	finalBytes, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	a, err := auth.ParseFile(full, finalBytes)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "parse: " + err.Error()})
		return
	}
	if err := os.WriteFile(full, finalBytes, 0600); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.pool.AddOAuth(a)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": a.ID})
}

type oauthStartBody struct {
	Provider string `json:"provider"` // "anthropic" | "openai"; empty = anthropic (back-compat)
	ProxyURL string `json:"proxy_url"`
	Label    string `json:"label"`
}

func (h *Handler) handleOAuthStart(c *gin.Context) {
	var body oauthStartBody
	_ = c.ShouldBindJSON(&body)
	provider := auth.NormalizeProvider(body.Provider)
	sess, authURL, err := auth.StartLogin(provider, body.ProxyURL, body.Label)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"session_id":   sess.ID,
		"provider":     provider,
		"auth_url":     authURL,
		"redirect_uri": auth.RedirectURIFor(provider),
	})
}

type oauthFinishBody struct {
	SessionID     string `json:"session_id"`
	Callback      string `json:"callback"` // full URL, or "code#state", or raw code
	Code          string `json:"code"`
	State         string `json:"state"`
	MaxConcurrent int    `json:"max_concurrent"`
	Group         string `json:"group"`
}

func (h *Handler) handleOAuthFinish(c *gin.Context) {
	var body oauthFinishBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(body.SessionID) == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing session_id"})
		return
	}
	code := strings.TrimSpace(body.Code)
	state := strings.TrimSpace(body.State)
	if code == "" && strings.TrimSpace(body.Callback) != "" {
		parsedCode, parsedState, err := auth.ParseCallback(body.Callback)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		code = parsedCode
		if state == "" {
			state = parsedState
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	a, err := auth.FinishLogin(ctx, body.SessionID, code, state, h.cfg.AuthDir, body.MaxConcurrent, h.cfg.UseUTLS, body.Group)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	h.pool.AddOAuth(a)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": a.ID, "email": a.Email})
}

// ---- API key CRUD ----

type createAPIKeyBody struct {
	Provider string            `json:"provider"` // "anthropic" | "openai"; empty = anthropic
	APIKey   string            `json:"api_key"`
	Label    string            `json:"label"`
	ProxyURL string            `json:"proxy_url"`
	BaseURL  string            `json:"base_url"`
	Filename string            `json:"filename"`
	Group    string            `json:"group"`
	ModelMap map[string]string `json:"model_map"`
}

func (h *Handler) handleCreateAPIKey(c *gin.Context) {
	var body createAPIKeyBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	key := strings.TrimSpace(body.APIKey)
	if key == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing api_key"})
		return
	}
	label := strings.TrimSpace(body.Label)
	name := sanitizeFilename(body.Filename)
	if name == "" {
		if label != "" {
			name = sanitizeFilename("apikey-"+label) + ".json"
		} else {
			name = fmt.Sprintf("apikey-%d.json", time.Now().Unix())
		}
	}
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		name += ".json"
	}
	if err := os.MkdirAll(h.cfg.AuthDir, 0700); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	full := filepath.Join(h.cfg.AuthDir, name)
	provider := auth.NormalizeProvider(body.Provider)
	typ := "apikey"
	if provider == auth.ProviderOpenAI {
		typ = "openai_api_key"
	}
	raw := map[string]any{
		"type":     typ,
		"provider": provider,
		"api_key":  key,
	}
	if label != "" {
		raw["label"] = label
	}
	if strings.TrimSpace(body.ProxyURL) != "" {
		raw["proxy_url"] = strings.TrimSpace(body.ProxyURL)
	}
	if strings.TrimSpace(body.BaseURL) != "" {
		raw["base_url"] = strings.TrimRight(strings.TrimSpace(body.BaseURL), "/")
	}
	if g := auth.NormalizeGroup(body.Group); g != "" {
		raw["group"] = g
	}
	if len(body.ModelMap) > 0 {
		mm := make(map[string]any, len(body.ModelMap))
		for k, v := range body.ModelMap {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			mm[k] = strings.TrimSpace(v)
		}
		if len(mm) > 0 {
			raw["model_map"] = mm
		}
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	a, err := auth.ParseFile(full, data)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := os.WriteFile(full, data, 0600); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.pool.AddAPIKey(a)
	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": a.ID})
}

// ---- Anthropic upstream usage proxy ----

func (h *Handler) handleAnthropicUsage(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.FindByID(id)
	if a == nil || a.Kind != auth.KindOAuth {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "oauth credential not found"})
		return
	}
	// Endpoint only speaks Anthropic's OAuth usage API — reject Codex
	// credentials up front rather than 502ing after a pointless token
	// refresh and an unknown-host probe.
	if auth.NormalizeProvider(a.Provider) != auth.ProviderAnthropic {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "anthropic-usage endpoint is Anthropic-only"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	// Ensure the access token is fresh before hitting the upstream endpoints.
	if err := a.EnsureFresh(ctx, 5*time.Minute, h.pool.UseUTLS()); err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "token refresh: " + err.Error()})
		return
	}
	token, _ := a.Credentials()
	client := auth.ClientFor(a.Snapshot().ProxyURL, h.pool.UseUTLS())

	fetch := func(url string) (int, map[string]any, string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, nil, err.Error()
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Anthropic-Beta", "oauth-2025-04-20")
		req.Header.Set("Accept-Encoding", "identity")
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err.Error()
		}
		defer resp.Body.Close()
		buf, _ := io.ReadAll(resp.Body)
		var obj map[string]any
		_ = json.Unmarshal(buf, &obj)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			msg := strings.TrimSpace(string(buf))
			if len(msg) > 300 {
				msg = msg[:300] + "...(truncated)"
			}
			return resp.StatusCode, obj, msg
		}
		return resp.StatusCode, obj, ""
	}

	usageStatus, usageBody, usageErr := fetch("https://api.anthropic.com/api/oauth/usage")
	profileStatus, profileBody, profileErr := fetch("https://api.anthropic.com/api/oauth/profile")

	c.JSON(http.StatusOK, gin.H{
		"usage": gin.H{
			"status": usageStatus,
			"body":   usageBody,
			"error":  usageErr,
		},
		"profile": gin.H{
			"status": profileStatus,
			"body":   profileBody,
			"error":  profileErr,
		},
	})
}

// handleCodexUsage actively probes chatgpt.com/backend-api/wham/usage for an
// OpenAI OAuth credential. The same data is mirrored into a.CodexRateLimits
// so the legacy "Rolling 5h / weekly" panel stays in sync; the full payload
// (plan_type / credits / spend_control) is returned to the caller for the
// admin dialog. See cc-core/auth/codex_usage.go for the response shape.
func (h *Handler) handleCodexUsage(c *gin.Context) {
	id := c.Param("id")
	a := h.pool.FindByID(id)
	if a == nil || a.Kind != auth.KindOAuth {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "oauth credential not found"})
		return
	}
	if auth.NormalizeProvider(a.Provider) != auth.ProviderOpenAI {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "codex-usage endpoint is OpenAI-only"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	info, err := a.FetchCodexUsage(ctx, h.pool.UseUTLS())
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": info})
}

// ---- request log query ----

func (h *Handler) handleRequestsHourly(c *gin.Context) {
	if h.cfg.LogDir == "" {
		c.JSON(http.StatusOK, gin.H{"buckets": []requestlog.HourBucket{}})
		return
	}
	hours := 24
	if v := strings.TrimSpace(c.Query("hours")); v != "" {
		fmt.Sscanf(v, "%d", &hours)
		if hours < 1 {
			hours = 1
		}
		if hours > 168 {
			hours = 168
		}
	}
	buckets, err := requestlog.AggregateHourly(h.cfg.LogDir, hours)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"buckets": buckets})
}

func (h *Handler) handleRequestsClients(c *gin.Context) {
	// Return current token names (no log scan). Orphan names that exist only
	// in historical logs are not offered as filter options; the user can
	// still type the old name into the URL if needed.
	seen := make(map[string]struct{})
	if h.tokens != nil {
		for _, t := range h.tokens.List() {
			n := strings.TrimSpace(t.Name)
			if n == "" {
				continue
			}
			seen[n] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	c.JSON(http.StatusOK, gin.H{"clients": out})
}

func (h *Handler) handleRequestsQuery(c *gin.Context) {
	if h.cfg.LogDir == "" {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "log_dir not configured"})
		return
	}
	f := requestlog.Filter{
		Dir:   h.cfg.LogDir,
		Model: strings.TrimSpace(c.Query("model")),
	}
	// Resolve the `client` query param (a current token name) to the masked
	// token so the filter matches across renames. Fall back to string match
	// on Record.Client for orphan names that no longer resolve.
	if name := strings.TrimSpace(c.Query("client")); name != "" {
		var matched string
		if h.tokens != nil {
			for _, t := range h.tokens.List() {
				if strings.EqualFold(strings.TrimSpace(t.Name), name) {
					matched = maskToken(t.Token)
					break
				}
			}
		}
		if matched != "" {
			f.ClientToken = matched
		} else {
			f.Client = name
		}
	}
	if v := strings.TrimSpace(c.Query("from")); v != "" {
		if t, err := parseDateBound(v, false); err == nil {
			f.From = t
		}
	}
	if v := strings.TrimSpace(c.Query("to")); v != "" {
		if t, err := parseDateBound(v, true); err == nil {
			f.To = t
		}
	}
	if v := c.Query("limit"); v != "" {
		fmt.Sscanf(v, "%d", &f.Limit)
	}
	if v := c.Query("offset"); v != "" {
		fmt.Sscanf(v, "%d", &f.Offset)
	}
	if v := c.Query("status"); v != "" {
		fmt.Sscanf(v, "%d", &f.Status)
	}
	res, err := h.cachedQuery(f)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.remapDisplayNames(res.Entries)
	res.ByClient = h.remapByClient(res.ByClient)
	c.JSON(http.StatusOK, res)
}

// cachedQuery returns a fresh shallow copy of the cached Result for the
// given filter. Aggregate maps (ByClient/ByModel/ByDay) and Summary are
// shared with the cache — callers must replace them, not mutate in place.
// Entries are cloned into a fresh slice so downstream remapDisplayNames
// can mutate without data-racing concurrent readers.
func (h *Handler) cachedQuery(f requestlog.Filter) (*requestlog.Result, error) {
	key := reqCacheKey(f)
	h.reqCacheMu.Lock()
	if ent, ok := h.reqCache[key]; ok && time.Since(ent.at) < requestsCacheTTL {
		clone := cloneResult(ent.result)
		h.reqCacheMu.Unlock()
		return clone, nil
	}
	h.reqCacheMu.Unlock()

	res, err := requestlog.Query(f)
	if err != nil {
		return nil, err
	}

	h.reqCacheMu.Lock()
	if h.reqCache == nil || len(h.reqCache) >= requestsCacheMax {
		// Coarse eviction: when the cache grows unbounded (e.g., varied
		// user filters from the Requests tab), drop everything. The hot
		// Overview polls refill the two common keys within 10s.
		h.reqCache = make(map[string]reqCacheEntry, 4)
	}
	h.reqCache[key] = reqCacheEntry{at: time.Now(), result: res}
	clone := cloneResult(res)
	h.reqCacheMu.Unlock()
	return clone, nil
}

func reqCacheKey(f requestlog.Filter) string {
	// Dir is constant per process; skip it to keep keys short.
	return fmt.Sprintf("%s|%s|%s|%s|%s|%d|%d|%d",
		f.From.UTC().Format(time.RFC3339),
		f.To.UTC().Format(time.RFC3339),
		f.Client, f.ClientToken, f.Model,
		f.Status, f.Limit, f.Offset,
	)
}

func cloneResult(r *requestlog.Result) *requestlog.Result {
	if r == nil {
		return nil
	}
	out := *r
	if r.Entries != nil {
		out.Entries = append([]requestlog.Record(nil), r.Entries...)
	}
	return &out
}

// remapByClient rewrites ByClient map keys from masked ClientToken to the
// current display name. Unknown masks (deleted tokens) fall back to the
// mask itself so they remain visible as orphan rows. Merges buckets if two
// masks ever map to the same name (shouldn't happen in practice).
func (h *Handler) remapByClient(in map[string]requestlog.Aggregate) map[string]requestlog.Aggregate {
	if len(in) == 0 {
		return in
	}
	nameByMasked := make(map[string]string)
	if h.tokens != nil {
		for _, t := range h.tokens.List() {
			n := strings.TrimSpace(t.Name)
			if n == "" {
				continue
			}
			nameByMasked[maskToken(t.Token)] = n
		}
	}
	out := make(map[string]requestlog.Aggregate, len(in))
	for k, v := range in {
		display := k
		if cur, ok := nameByMasked[k]; ok {
			display = cur
		}
		if existing, ok := out[display]; ok {
			existing.Count += v.Count
			existing.InputTokens += v.InputTokens
			existing.OutputTokens += v.OutputTokens
			existing.CacheReadTokens += v.CacheReadTokens
			existing.CacheCreateTokens += v.CacheCreateTokens
			existing.CostUSD += v.CostUSD
			existing.Errors += v.Errors
			existing.TotalDurationMs += v.TotalDurationMs
			out[display] = existing
		} else {
			out[display] = v
		}
	}
	return out
}

// remapDisplayNames rewrites snapshot display fields on log entries to their
// current values. The log is append-only and stores a point-in-time snapshot
// of the client name and auth label; when either is renamed, the UI should
// reflect the new name even for historical rows. Audit correlation stays
// keyed by stable IDs (ClientToken masked form, AuthID), which the log also
// carries untouched. If an ID no longer resolves (token / auth deleted), the
// snapshot is left in place as the last known display value.
func (h *Handler) remapDisplayNames(entries []requestlog.Record) {
	if len(entries) == 0 {
		return
	}
	nameByMasked := make(map[string]string)
	if h.tokens != nil {
		for _, t := range h.tokens.List() {
			n := strings.TrimSpace(t.Name)
			if n == "" {
				continue
			}
			nameByMasked[maskToken(t.Token)] = n
		}
	}
	var labelIdx map[string]auth.AuthLabelInfo
	if h.pool != nil {
		labelIdx = h.pool.LabelIndex()
	}
	for i := range entries {
		if cur, ok := nameByMasked[entries[i].ClientToken]; ok {
			entries[i].Client = cur
		}
		if entries[i].AuthID != "" && labelIdx != nil {
			if cur, ok := labelIdx[entries[i].AuthID]; ok {
				entries[i].AuthLabel = cur.Label
				entries[i].AuthKind = authKindString(cur.Kind)
			}
		}
	}
}

// parseDateBound accepts "YYYY-MM-DD" (start-of-day) or full RFC3339.
// endOfDay=true shifts bare dates to 23:59:59 so `to=2026-04-14` covers
// the whole day.
func parseDateBound(s string, endOfDay bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, err
	}
	if endOfDay {
		return t.Add(24*time.Hour - time.Second), nil
	}
	return t, nil
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "..", "")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}

// ---- Client access token CRUD ----

// tokenView is the shape returned to the panel. The token itself is masked
// unless the caller asks for the full value (`?full=1`), in which case
// every row is returned verbatim — used by the "copy to clipboard" button
// in the Add-token modal right after creation.
type tokenView struct {
	Token         string     `json:"token"`
	Masked        string     `json:"masked"`
	Name          string     `json:"name"`
	WeeklyUSD     float64    `json:"weekly_usd"`
	MaxConcurrent int        `json:"max_concurrent,omitempty"`
	RPM           int        `json:"rpm,omitempty"`
	Group         string     `json:"group,omitempty"`
	Groups        []string   `json:"groups,omitempty"` // priority-ordered fallthrough channel list
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	// Live usage for the current ISO week, convenient for the panel row.
	WeeklyUsedUSD float64 `json:"weekly_used_usd"`
}

func (h *Handler) handleListTokens(c *gin.Context) {
	full := c.Query("full") == "1"
	rows := h.tokens.List()
	out := make([]tokenView, 0, len(rows))
	for _, t := range rows {
		v := tokenView{
			Masked:        maskToken(t.Token),
			Name:          t.Name,
			WeeklyUSD:     t.WeeklyUSD,
			MaxConcurrent: t.MaxConcurrent,
			RPM:           t.RPM,
			Group:         t.Group,
			Groups:        append([]string(nil), t.Groups...),
			WeeklyUsedUSD: h.usage.WeeklyCostUSD(t.Token),
		}
		if !t.CreatedAt.IsZero() {
			ct := t.CreatedAt
			v.CreatedAt = &ct
		}
		if full {
			v.Token = t.Token
		}
		out = append(out, v)
	}
	c.JSON(http.StatusOK, gin.H{"tokens": out})
}

type createTokenBody struct {
	Token         string   `json:"token"`
	Name          string   `json:"name"`
	WeeklyUSD     float64  `json:"weekly_usd"`
	MaxConcurrent int      `json:"max_concurrent,omitempty"`
	RPM           int      `json:"rpm,omitempty"`
	Group         string   `json:"group,omitempty"`
	Groups        []string `json:"groups,omitempty"` // priority-ordered fallthrough list
	Generate      bool     `json:"generate"`         // if true and Token == "", mint a fresh sk-...
}

func (h *Handler) handleCreateToken(c *gin.Context) {
	var body createTokenBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tok := strings.TrimSpace(body.Token)
	if tok == "" {
		if !body.Generate {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "token required (or set generate:true)"})
			return
		}
		v, err := clienttoken.Generate()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "generate: " + err.Error()})
			return
		}
		tok = v
	}
	entry := clienttoken.Token{
		Token:         tok,
		Name:          body.Name,
		WeeklyUSD:     body.WeeklyUSD,
		MaxConcurrent: body.MaxConcurrent,
		RPM:           body.RPM,
		Group:         body.Group,
		Groups:        body.Groups,
	}
	if err := h.tokens.Add(entry); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"token":      tok, // return the full value once so the panel can show it
		"name":       body.Name,
		"weekly_usd": body.WeeklyUSD,
	})
}

type patchTokenBody struct {
	Name          *string   `json:"name"`
	WeeklyUSD     *float64  `json:"weekly_usd"`
	MaxConcurrent *int      `json:"max_concurrent"`
	RPM           *int      `json:"rpm"`
	Group         *string   `json:"group"`
	Groups        *[]string `json:"groups"` // priority-ordered fallthrough list; nil = unchanged
}

func (h *Handler) handlePatchToken(c *gin.Context) {
	tok := c.Param("token")
	var body patchTokenBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.tokens.Update(tok, body.Name, body.WeeklyUSD, body.MaxConcurrent, body.RPM, body.Group, body.Groups, nil); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) handleDeleteToken(c *gin.Context) {
	tok := c.Param("token")
	if err := h.tokens.Delete(tok); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// orphanToken is a client token that appears in recorded usage but is
// not in the clienttoken store (deleted or never registered). Exposed to
// admins so the Edit dialog can offer a "inherit usage from …" merge.
type orphanToken struct {
	Token    string           `json:"token"`
	Masked   string           `json:"masked"`
	Label    string           `json:"label,omitempty"`
	Total    usage.ClientCost `json:"total"`
	LastUsed *time.Time       `json:"last_used,omitempty"`
}

func (h *Handler) handleListOrphanTokens(c *gin.Context) {
	registered := make(map[string]bool)
	for _, t := range h.tokens.List() {
		registered[t.Token] = true
	}
	out := make([]orphanToken, 0)
	for tok, pc := range h.usage.SnapshotClients() {
		if registered[tok] {
			continue
		}
		row := orphanToken{
			Token:  tok,
			Masked: maskToken(tok),
			Label:  pc.Label,
			Total:  pc.Total,
		}
		if !pc.LastUsed.IsZero() {
			lu := pc.LastUsed
			row.LastUsed = &lu
		}
		out = append(out, row)
	}
	c.JSON(http.StatusOK, gin.H{"orphans": out})
}

func (h *Handler) handleResetToken(c *gin.Context) {
	tok := c.Param("token")
	newTok, err := clienttoken.Generate()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "generate: " + err.Error()})
		return
	}
	if err := h.tokens.Reset(tok, newTok); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Rename usage after the token store commits; if this fails the old
	// usage history stays under the old key (surfaces as orphan), but the
	// new token already works — non-fatal.
	if err := h.usage.RenameClient(tok, newTok); err != nil {
		log.Warnf("admin: reset usage rename %s→%s: %v", maskToken(tok), maskToken(newTok), err)
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "token": newTok})
}

type inheritTokenBody struct {
	From string `json:"from"`
}

func (h *Handler) handleInheritToken(c *gin.Context) {
	dst := c.Param("token")
	var body inheritTokenBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	src := strings.TrimSpace(body.From)
	if src == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "from required"})
		return
	}
	if _, ok := h.tokens.Lookup(dst); !ok {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "destination token not registered"})
		return
	}
	// Refuse merging from a still-registered token: it's either a mistake
	// or the caller should delete the source explicitly first.
	if _, ok := h.tokens.Lookup(src); ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "source token is still registered; delete it first or pick an orphan"})
		return
	}
	if err := h.usage.MergeClient(src, dst); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// RegisterSaaSBridge mounts the legacy admin handlers that the SaaS UI needs
// (request-log queries + Anthropic OAuth quota probe) on the supplied group.
// Caller is responsible for attaching the right auth middleware — this method
// does NOT install the legacy admin_token gate. The expected use is binding
// it under /api/v2/admin/* after the SaaS RequireAdmin middleware.
func (h *Handler) RegisterSaaSBridge(g *gin.RouterGroup) {
	g.GET("/requests", h.handleRequestsQuery)
	g.GET("/requests/clients", h.handleRequestsClients)
	g.GET("/requests/hourly", h.handleRequestsHourly)
	g.POST("/credentials/:id/anthropic-usage", h.handleAnthropicUsage)
	g.POST("/credentials/:id/codex-usage", h.handleCodexUsage)
	// Rich credential read + mutations. Mirrors the legacy /summary fields so
	// the SaaS panel can render the same usage / quota / model_map / sparkline
	// data that the operator panel does. The saas/admin CredHandler still owns
	// create / oauth-start / oauth-finish / delete — those are not just
	// pass-throughs (they write JSON files into AuthDir).
	g.GET("/credentials", h.handleBridgeListCreds)
	// Upload a raw credential JSON (export from another instance, etc.). The
	// legacy handler writes the file into AuthDir and adds it to the shared
	// pool — the same pool the SaaS panel reads — so it bridges cleanly.
	g.POST("/credentials/upload", h.handleUpload)
	g.PATCH("/credentials/:id", h.handlePatchAuth)
	g.POST("/credentials/:id/refresh", h.handleRefresh)
	g.POST("/credentials/:id/clear-quota", h.handleClearQuota)
	g.POST("/credentials/:id/clear-failure", h.handleClearFailure)
	// Kiro side-channel mirror: same /api/kiro/* surface exposed under
	// /api/v2/admin/kiro/* so the SaaS panel can manage Kiro creds with the
	// SaaS JWT (instead of a legacy admin token). No-op when Kiro disabled.
	if h.kiro != nil {
		kg := g.Group("/kiro")
		kg.GET("/credentials", h.handleKiroList)
		kg.PATCH("/credentials/:id", h.handleKiroPatch)
		kg.DELETE("/credentials/:id", h.handleKiroDelete)
		kg.GET("/credentials/:id/credits", h.handleKiroCredits)
		kg.POST("/login/start", h.handleKiroLoginStart)
		kg.POST("/login/finish", h.handleKiroLoginFinish)
	}
}

// handleBridgeListCreds emits the rich credential rows for the SaaS panel
// (/api/v2/admin/credentials). Same shape as /summary's "auths" but without
// the clients/pricing fan-out — those have their own endpoints elsewhere.
func (h *Handler) handleBridgeListCreds(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"credentials": h.buildAuthRows()})
}
