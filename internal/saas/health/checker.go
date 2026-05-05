// Package health probes API-key credentials and tracks per-(auth,model)
// availability. OAuth credentials are NOT probed — they have their own
// health state on auth.Auth.IsHealthy() that the existing proxy maintains
// from real traffic.
package health

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

type Checker struct {
	DB       *db.DB
	Pool     *auth.Pool
	Models   []ModelTarget
	Interval time.Duration
	mu       sync.Mutex
	running  bool
}

// ModelTarget pairs a provider with a model name to ping.
type ModelTarget struct {
	Provider string
	Model    string
}

// DefaultTargets returns a sensible probe set.
func DefaultTargets() []ModelTarget {
	return []ModelTarget{
		{Provider: auth.ProviderAnthropic, Model: "claude-haiku-4-5"},
		{Provider: auth.ProviderAnthropic, Model: "claude-sonnet-4-6"},
		{Provider: auth.ProviderOpenAI, Model: "gpt-5.3-codex"},
	}
}

func New(store *db.DB, pool *auth.Pool, models []ModelTarget, interval time.Duration) *Checker {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	if len(models) == 0 {
		models = DefaultTargets()
	}
	return &Checker{DB: store, Pool: pool, Models: models, Interval: interval}
}

// Run starts the periodic checker. Cancel ctx to stop.
func (c *Checker) Run(ctx context.Context) {
	t := time.NewTicker(c.Interval)
	defer t.Stop()
	c.RunOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.RunOnce(ctx)
		}
	}
}

// RunOnce probes every target once. Safe to call from /api/v2/admin/health/refresh.
func (c *Checker) RunOnce(ctx context.Context) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.running = false; c.mu.Unlock() }()

	for _, st := range c.Pool.Status() {
		if st.Auth.Kind != auth.KindAPIKey {
			continue
		}
		a := c.Pool.FindByID(st.Auth.ID)
		if a == nil {
			continue
		}
		for _, m := range c.Models {
			if auth.NormalizeProvider(m.Provider) != auth.NormalizeProvider(a.Provider) {
				continue
			}
			c.checkOne(ctx, a, m)
		}
	}
}

// Refresh kicks off RunOnce in the background. Used by admin trigger.
func (c *Checker) Refresh() { go c.RunOnce(context.Background()) }

func (c *Checker) checkOne(ctx context.Context, a *auth.Auth, m ModelTarget) {
	t := time.Now()
	st, errMsg := c.probe(ctx, a, m)
	rec := db.ModelHealth{
		AuthID:    a.ID,
		Provider:  auth.NormalizeProvider(m.Provider),
		Model:     m.Model,
		Status:    st,
		LatencyMs: int(time.Since(t).Milliseconds()),
		Error:     errMsg,
	}
	if err := c.DB.UpsertModelHealth(ctx, rec); err != nil {
		log.Warnf("health: upsert %s/%s: %v", a.ID, m.Model, err)
	}
}

func (c *Checker) probe(ctx context.Context, a *auth.Auth, m ModelTarget) (status, errMsg string) {
	cli := auth.ClientFor(a.ProxyURL, false)
	cliCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	switch auth.NormalizeProvider(a.Provider) {
	case auth.ProviderAnthropic:
		body := map[string]any{
			"model":      m.Model,
			"max_tokens": 1,
			"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
		}
		buf, _ := json.Marshal(body)
		base := strings.TrimRight(a.Snapshot().BaseURL, "/")
		if base == "" {
			base = "https://api.anthropic.com"
		}
		req, _ := http.NewRequestWithContext(cliCtx, http.MethodPost, base+"/v1/messages", bytes.NewReader(buf))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.AccessToken)
		req.Header.Set("anthropic-version", "2023-06-01")
		resp, err := cli.Do(req)
		if err != nil {
			return "fail", err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode < 400 {
			return "ok", ""
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return "fail", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	case auth.ProviderOpenAI:
		body := map[string]any{
			"model":      m.Model,
			"max_tokens": 1,
			"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
		}
		buf, _ := json.Marshal(body)
		base := strings.TrimRight(a.Snapshot().BaseURL, "/")
		if base == "" {
			base = "https://api.openai.com"
		}
		req, _ := http.NewRequestWithContext(cliCtx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(buf))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+a.AccessToken)
		resp, err := cli.Do(req)
		if err != nil {
			return "fail", err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode < 400 {
			return "ok", ""
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return "fail", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return "fail", "unknown provider"
}
