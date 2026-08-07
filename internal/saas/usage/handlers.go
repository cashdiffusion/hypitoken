// Package usage serves the spend-analytics endpoints: per-key / per-model /
// per-day rollups of the charge ledger, plus CSV export.
//
// It backs two views off one implementation:
//
//   - PERSONAL (/api/v2/me/usage/*, RequireUser) — scoped to the calling user, so
//     an individual can break their own spend down by key.
//   - TEAM (/api/v2/workspaces/:id/usage/*, RequireWorkspaceAdmin) — scoped to one
//     workspace, so a company can see what each employee's key cost.
//
// The scope is NEVER read from a query parameter. Personal takes it from the
// authenticated user; team takes it from the path param the RequireWorkspaceAdmin
// middleware has already verified the caller administers. Everything downstream
// runs through db.ReportFilter, whose SQL always emits a scope predicate.
//
// Nothing here can expose upstream credentials: wallet_tx simply has no auth_id,
// credential label or provider column. Key secrets are never selected either — the
// reports carry a key's id, name and tags, never its token.
package usage

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

type Handler struct {
	DB *db.DB
	// summaryCache memoizes the assembled /summary body per report window.
	// See cache.go for why this endpoint in particular needs one.
	summaryCache *respCache
}

func New(store *db.DB) *Handler { return &Handler{DB: store, summaryCache: newRespCache()} }

// PersonalRoutes mounts the individual view on a group already wrapped with
// RequireUser (rooted at /me/usage).
func (h *Handler) PersonalRoutes(g *gin.RouterGroup) {
	g.GET("/summary", h.scoped(personalScope, h.summary))
	g.GET("/rows", h.scoped(personalScope, h.rows))
	g.GET("/export.csv", h.scoped(personalScope, h.exportCSV))
}

// TeamRoutes mounts the workspace view on a group already wrapped with
// RequireWorkspaceAdmin (rooted at /workspaces/:id/usage).
func (h *Handler) TeamRoutes(g *gin.RouterGroup) {
	g.GET("/summary", h.scoped(teamScope, h.summary))
	g.GET("/tokens", h.scoped(teamScope, h.tokens))
	g.GET("/rows", h.scoped(teamScope, h.rows))
	g.GET("/export.csv", h.scoped(teamScope, h.exportCSV))
}

// scopeFn derives the billing subject from the request. Returning ok=false means
// the request is not authorized for any scope and must be rejected — never
// silently widened.
type scopeFn func(*gin.Context) (db.Scope, bool)

func personalScope(c *gin.Context) (db.Scope, bool) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		return db.Scope{}, false
	}
	return db.UserScope(u.ID), true
}

func teamScope(c *gin.Context) (db.Scope, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return db.Scope{}, false
	}
	// RequireWorkspaceAdmin has already established the caller administers this
	// workspace; we only have to trust the path param it validated.
	return db.WorkspaceScope(id), true
}

// scoped resolves the scope, parses the shared filter, and hands both to the
// handler. Centralizing this is what makes it impossible for one endpoint to
// forget the scope predicate.
func (h *Handler) scoped(scope scopeFn, fn func(*gin.Context, db.ReportFilter)) gin.HandlerFunc {
	return func(c *gin.Context) {
		s, ok := scope(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}
		f, err := parseFilter(c, s)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// A personal caller may only ever ask about their OWN keys. The scope
		// predicate already guarantees the numbers are right (someone else's key
		// simply matches zero rows), but a 404 is a much clearer answer than a
		// silently empty chart.
		if f.TokenID > 0 && s.UserID > 0 {
			t, err := h.DB.GetUserToken(c.Request.Context(), f.TokenID)
			if err != nil || t.UserID != s.UserID {
				c.JSON(http.StatusNotFound, gin.H{"error": "token not found"})
				return
			}
		}
		fn(c, f)
	}
}

const (
	defaultWindow = 90 * 24 * time.Hour
	maxWindow     = 366 * 24 * time.Hour
)

// parseFilter reads the shared query params. The window is bounded: an unbounded
// scan of a busy workspace's whole ledger is both slow and never what anyone
// actually wants.
func parseFilter(c *gin.Context, s db.Scope) (db.ReportFilter, error) {
	f := db.ReportFilter{Scope: s}

	// `to` is INCLUSIVE on the wire — a human asking for "…to July 10" means the
	// whole of July 10 — but the SQL bound is half-open, so we widen it by a day.
	// This is also what makes a single-day drill-down (from == to, e.g. clicking
	// one cell of the heatmap) a valid one-day window rather than an empty one.
	now := time.Now().UTC()
	toDay, err := parseDay(c.Query("to"), now.Truncate(24*time.Hour))
	if err != nil {
		return f, errBadParam("to")
	}
	to := toDay.AddDate(0, 0, 1)

	from, err := parseDay(c.Query("from"), to.Add(-defaultWindow))
	if err != nil {
		return f, errBadParam("from")
	}
	if from.After(toDay) {
		return f, errBadParam("from must not be after to")
	}
	if to.Sub(from) > maxWindow {
		return f, errBadParam("range exceeds 366 days")
	}
	f.From, f.To = from, to

	if v := c.Query("token_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return f, errBadParam("token_id")
		}
		f.TokenID = id
	}
	f.Model = strings.TrimSpace(c.Query("model"))
	f.Tag = strings.TrimSpace(c.Query("tag"))
	return f, nil
}

// parseDay accepts YYYY-MM-DD (UTC), falling back when the param is absent.
func parseDay(s string, fallback time.Time) (time.Time, error) {
	if s == "" {
		return fallback, nil
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, err
	}
	return d.UTC(), nil
}

type badParam string

func (e badParam) Error() string { return string(e) }

func errBadParam(what string) error { return badParam("bad " + what) }

// paginate reads limit/offset with sane bounds.
func paginate(c *gin.Context) (limit, offset int) {
	limit, _ = strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ = strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset = max(offset, 0)
	return limit, offset
}

// --- responses -------------------------------------------------------------

type tokenSpendView struct {
	TokenID int64    `json:"token_id"`
	Name    string   `json:"name"`
	Tags    []string `json:"tags"`
	UserID  int64    `json:"user_id,omitempty"`
	Email   string   `json:"email,omitempty"`
	Deleted bool     `json:"deleted,omitempty"`

	SpentUSD     float64 `json:"spent_usd"`
	ChargeEvents int64   `json:"charge_events"`

	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	CacheReadTokens   int64 `json:"cache_read_tokens"`
	CacheCreateTokens int64 `json:"cache_create_tokens"`

	LastAt int64 `json:"last_at,omitempty"`
}

func toTokenView(s *db.TokenSpend, withMember bool) tokenSpendView {
	v := tokenSpendView{
		TokenID: s.TokenID, Name: s.Name, Tags: s.Tags, Deleted: s.Deleted,
		SpentUSD: s.SpentUSD, ChargeEvents: s.ChargeEvents,
		InputTokens: s.InputTokens, OutputTokens: s.OutputTokens,
		CacheReadTokens: s.CacheReadTokens, CacheCreateTokens: s.CacheCreateTokens,
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}
	if withMember {
		v.UserID, v.Email = s.UserID, s.Email
	}
	if !s.LastAt.IsZero() {
		v.LastAt = s.LastAt.Unix()
	}
	return v
}

// summary is the dashboard payload: headline totals, the three breakdowns, the
// daily series that feeds the heatmap, and the streak derived from it.
func (h *Handler) summary(c *gin.Context, f db.ReportFilter) {
	body, err := h.summaryCache.do(filterKey("summary", f), func() (any, error) {
		return h.buildSummary(c.Request.Context(), f)
	})
	if err != nil {
		serverError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

func (h *Handler) buildSummary(ctx context.Context, f db.ReportFilter) (any, error) {
	withMember := f.WorkspaceID > 0

	// One pass, not five. Every breakdown here derives from the same slice of
	// the ledger; running them as independent queries meant scanning it five
	// times. See db.SpendBreakdown.
	b, err := h.DB.SpendBreakdown(ctx, f)
	if err != nil {
		return nil, err
	}
	totals, byToken, byModel, byTag, byDay := b.Total, b.ByToken, b.ByModel, b.ByTag, b.ByDay

	// The heatmap wants a dense series; the streak wants the same data, so both
	// come off one query.
	dense := db.ZeroFillDays(byDay, f.From, f.To.AddDate(0, 0, -1))
	current, longest := db.ComputeStreaks(byDay, time.Now())

	tokens := make([]tokenSpendView, 0, len(byToken))
	for _, t := range byToken {
		tokens = append(tokens, toTokenView(t, withMember))
	}

	return gin.H{
		"range": gin.H{
			"from": f.From.Format("2006-01-02"),
			"to":   f.To.AddDate(0, 0, -1).Format("2006-01-02"),
		},
		"total": gin.H{
			"spent_usd":           totals.SpentUSD,
			"charge_events":       totals.ChargeEvents,
			"active_tokens":       totals.ActiveTokens,
			"input_tokens":        totals.InputTokens,
			"output_tokens":       totals.OutputTokens,
			"cache_read_tokens":   totals.CacheReadTokens,
			"cache_create_tokens": totals.CacheCreateTokens,
		},
		"by_token": tokens,
		"by_model": byModel,
		"by_tag":   byTag,
		"by_day":   dense,
		"streak":   gin.H{"current_days": current, "longest_days": longest},
	}, nil
}

// tokens is the team view's headline table: one row per key, across all members.
func (h *Handler) tokens(c *gin.Context, f db.ReportFilter) {
	byToken, err := h.DB.SpendByToken(c.Request.Context(), f)
	if err != nil {
		serverError(c, err)
		return
	}
	out := make([]tokenSpendView, 0, len(byToken))
	total := 0.0
	for _, t := range byToken {
		out = append(out, toTokenView(t, true))
		total += t.SpentUSD
	}
	c.JSON(http.StatusOK, gin.H{"tokens": out, "total_spent_usd": total})
}

type spendRowView struct {
	ID        int64    `json:"id"`
	CreatedAt int64    `json:"created_at"`
	TokenID   int64    `json:"token_id"`
	TokenName string   `json:"token_name"`
	TokenTags []string `json:"token_tags"`
	Email     string   `json:"email,omitempty"`
	Model     string   `json:"model"`
	AmountUSD float64  `json:"amount_usd"`

	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	CacheReadTokens   int64 `json:"cache_read_tokens"`
	CacheCreateTokens int64 `json:"cache_create_tokens"`

	// Attributed=false marks a pre-v15 row whose key/model couldn't be recovered
	// from the legacy ref. The UI greys these out rather than pretending they
	// cost nothing.
	Attributed bool `json:"attributed"`
}

func (h *Handler) rows(c *gin.Context, f db.ReportFilter) {
	f.Limit, f.Offset = paginate(c)
	withMember := f.WorkspaceID > 0

	rows, total, err := h.DB.ListSpendRows(c.Request.Context(), f)
	if err != nil {
		serverError(c, err)
		return
	}
	out := make([]spendRowView, 0, len(rows))
	for _, r := range rows {
		v := spendRowView{
			ID: r.ID, CreatedAt: r.CreatedAt.Unix(),
			TokenID: r.TokenID, TokenName: r.TokenName, TokenTags: r.TokenTags,
			Model: r.Model, AmountUSD: r.AmountUSD,
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
			CacheReadTokens: r.CacheReadTokens, CacheCreateTokens: r.CacheCreateTokens,
			Attributed: r.Attributed,
		}
		if v.TokenTags == nil {
			v.TokenTags = []string{}
		}
		if withMember {
			v.Email = r.Email
		}
		out = append(out, v)
	}
	c.JSON(http.StatusOK, gin.H{"rows": out, "total": total})
}

func serverError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
