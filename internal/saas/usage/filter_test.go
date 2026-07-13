package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// `to` is inclusive on the wire. Two things depend on it:
//
//   - Asking for "…to July 10" must INCLUDE July 10. Treating the wire value as
//     the half-open SQL bound silently dropped the last day of every range —
//     including, every day, today.
//   - from == to must be a valid one-day window, because that is exactly what
//     clicking a single cell of the heatmap sends.
func TestToDateIsInclusive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	d := testDB(t)

	u, err := d.CreateUser(ctx, "range@example.com", "h", "user", 1, true)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	ws, err := d.PersonalWorkspaceID(ctx, u.ID)
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	key, err := d.CreateUserToken(ctx, u.ID, db.TokenParams{Name: "k"})
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	// One charge, at noon UTC, three days ago.
	day := time.Now().UTC().AddDate(0, 0, -3).Truncate(24 * time.Hour)
	at := day.Add(12 * time.Hour)
	if _, err := d.ExecContext(ctx, `INSERT INTO wallet_tx
		(user_id, workspace_id, kind, amount_usd, ref, note, created_at, token_id, model)
		VALUES (?, ?, 'charge', -2.50, 'token=1 model=m', '', ?, ?, 'm')`,
		u.ID, ws, at.Unix(), key.ID); err != nil {
		t.Fatalf("seed charge: %v", err)
	}

	r := gin.New()
	g := r.Group("/me/usage")
	g.Use(func(c *gin.Context) {
		c.Set(string(saasauth.CtxUser), u)
		c.Next()
	})
	New(d).PersonalRoutes(g)

	spend := func(query string) float64 {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/me/usage/summary?"+query, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s → %d: %s", query, w.Code, w.Body.String())
		}
		var got struct {
			Total struct {
				SpentUSD float64 `json:"spent_usd"`
			} `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return got.Total.SpentUSD
	}

	key3 := day.Format("2006-01-02")
	prev := day.AddDate(0, 0, -1).Format("2006-01-02")

	// The charge's own day, as both bounds — the heatmap drill-down.
	if got := spend("from=" + key3 + "&to=" + key3); got != 2.50 {
		t.Errorf("single-day window (from=to=%s) = %v, want 2.50", key3, got)
	}
	// A range ENDING on the charge's day must include it.
	if got := spend("from=" + prev + "&to=" + key3); got != 2.50 {
		t.Errorf("range ending on the charge day = %v, want 2.50", got)
	}
	// A range ending the day BEFORE must not.
	if got := spend("from=" + prev + "&to=" + prev); got != 0 {
		t.Errorf("range ending before the charge day = %v, want 0", got)
	}
}

// A backwards range is a client bug, not an empty result set.
func TestFromAfterToRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	d := testDB(t)
	u, err := d.CreateUser(ctx, "bad@example.com", "h", "user", 1, true)
	if err != nil {
		t.Fatalf("user: %v", err)
	}

	r := gin.New()
	g := r.Group("/me/usage")
	g.Use(func(c *gin.Context) {
		c.Set(string(saasauth.CtxUser), u)
		c.Next()
	})
	New(d).PersonalRoutes(g)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/me/usage/summary?from=2026-07-10&to=2026-07-01", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("from > to → %d, want 400", w.Code)
	}
}
