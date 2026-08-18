// Package arena powers the public leaderboard + real-time "Agent office"
// visualisation. It is self-contained: it depends only on *db.DB (for the
// profile counters / leaderboard query) and the SaaS JWT Issuer (to auth
// the SSE stream, which cannot send an Authorization header from EventSource).
//
// The single integration point with the billing hot path is OnCharge, called
// from the SaaS adapter's Charge: it bumps the user's activity counters and
// publishes a pulse to every connected office. Everything runs off the request
// goroutine so the proxy is never blocked on arena work.
package arena

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// Service wires the hub to the DB + JWT issuer and exposes the HTTP routes.
type Service struct {
	DB  *db.DB
	Hub *Hub
	iss *saasauth.Issuer
}

// New constructs the arena service. ringSize is the number of recent events
// replayed to a freshly-connected office.
func New(store *db.DB, iss *saasauth.Issuer) *Service {
	return &Service{DB: store, Hub: NewHub(40), iss: iss}
}

// OnCharge is invoked by the billing adapter for every billed request. It is
// fire-and-forget: a goroutine bumps the counters and publishes the pulse so
// the proxy's request goroutine returns immediately. tokens is the request's
// total token count (input+output+cache).
func (s *Service) OnCharge(userID int64, provider, model string, tokens int64) {
	if s == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.DB.BumpActivity(ctx, userID, tokens)
		// Resolve public identity off the hot path.
		name := pseudonymFor(userID)
		public := false
		if p, err := s.DB.GetOrCreateProfile(ctx, userID); err == nil {
			name = displayName(userID, p.DisplayName, p.PublicOptIn)
			public = p.PublicOptIn
		}
		s.Hub.Publish(Event{
			ActorID:  actorIDFor(userID),
			Name:     name,
			Public:   public,
			Provider: provider,
			Model:    model,
			Tokens:   tokens,
			TS:       time.Now().UnixMilli(),
			userID:   userID,
		})
	}()
}

// AuthedRoutes registers routes that sit under the RequireUser group (header
// JWT). The leaderboard is a plain authed GET.
func (s *Service) AuthedRoutes(g *gin.RouterGroup) {
	g.GET("/arena/leaderboard", s.leaderboard)
}

// PublicRoutes registers routes that do their own auth. The SSE stream lives
// here because EventSource can't set an Authorization header, so it accepts the
// JWT via the `access_token` query parameter (falling back to the header).
func (s *Service) PublicRoutes(g *gin.RouterGroup) {
	g.GET("/arena/stream", s.stream)
}

type leaderRow struct {
	Rank     int    `json:"rank"`
	Actor    string `json:"actor"`
	Name     string `json:"name"`
	Public   bool   `json:"public"`
	IsYou    bool   `json:"is_you"`
	Tokens   int64  `json:"tokens"`
	Requests int64  `json:"requests"`
	Invites  int64  `json:"invites"`
	LastSeen int64  `json:"last_seen"` // unix seconds, 0 if never
}

func (s *Service) leaderboard(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	metric := db.MetricTokens
	if c.Query("metric") == "requests" {
		metric = db.MetricRequests
	}
	// "invites" is deliberately NOT selectable: the invite programme was farmed
	// for signup credit (2026-08-08: 168 signups, ~$116) and is suspended, so
	// the leaderboard must not rank — or advertise — inviting. The per-row
	// Invites field below stays as a frozen historical count.
	rows, err := s.DB.Leaderboard(c.Request.Context(), metric, 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]leaderRow, 0, len(rows))
	var you *leaderRow
	for i, r := range rows {
		lr := leaderRow{
			Rank:     i + 1,
			Actor:    actorIDFor(r.UserID),
			Name:     displayName(r.UserID, r.DisplayName, r.PublicOptIn),
			Public:   r.PublicOptIn,
			IsYou:    r.UserID == u.ID,
			Tokens:   r.LifetimeTokens,
			Requests: r.LifetimeRequests,
			Invites:  r.LifetimeInvites,
		}
		if !r.LastActiveAt.IsZero() {
			lr.LastSeen = r.LastActiveAt.Unix()
		}
		if lr.IsYou {
			cp := lr
			you = &cp
		}
		out = append(out, lr)
	}
	// If the viewer didn't make the top-100, still report their own rank/stats
	// so the page can show "you're #137".
	if you == nil {
		rank, _ := s.DB.RankOf(c.Request.Context(), u.ID, metric)
		if p, err := s.DB.GetOrCreateProfile(c.Request.Context(), u.ID); err == nil {
			yr := leaderRow{
				Rank:     rank,
				Actor:    actorIDFor(u.ID),
				Name:     displayName(u.ID, p.DisplayName, p.PublicOptIn),
				Public:   p.PublicOptIn,
				IsYou:    true,
				Tokens:   p.LifetimeTokens,
				Requests: p.LifetimeRequests,
				Invites:  p.LifetimeInvites,
			}
			if !p.LastActiveAt.IsZero() {
				yr.LastSeen = p.LastActiveAt.Unix()
			}
			you = &yr
		}
	}
	c.JSON(http.StatusOK, gin.H{"metric": string(metric), "rows": out, "you": you})
}

// streamUserID authenticates an SSE connection from either the Authorization
// header or the `access_token` query parameter (EventSource can't set headers).
func (s *Service) streamUserID(c *gin.Context) (int64, bool) {
	raw := strings.TrimSpace(c.GetHeader("Authorization"))
	if i := strings.IndexByte(raw, ' '); i > 0 {
		raw = strings.TrimSpace(raw[i+1:])
	}
	if raw == "" {
		raw = strings.TrimSpace(c.Query("access_token"))
	}
	if raw == "" {
		return 0, false
	}
	claims, err := s.iss.Parse(raw)
	if err != nil {
		return 0, false
	}
	return claims.UserID, true
}

func (s *Service) stream(c *gin.Context) {
	viewerID, ok := s.streamUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx/Caddy)

	flusher, ok := c.Writer.(interface{ Flush() })
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	id, ch := s.Hub.Subscribe()
	defer s.Hub.Unsubscribe(id)

	// Initial comment + replay of recent activity so the office isn't empty.
	fmt.Fprint(c.Writer, ": connected\n\n")
	for _, e := range s.Hub.Recent() {
		writeEvent(c.Writer, e, viewerID)
	}
	flusher.Flush()

	ctx := c.Request.Context()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			fmt.Fprint(c.Writer, ": keepalive\n\n")
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			writeEvent(c.Writer, e, viewerID)
			flusher.Flush()
		}
	}
}

// writeEvent serialises one event as SSE `data:`. is_you is computed per
// subscriber (the raw user id is never sent).
func writeEvent(w interface{ Write([]byte) (int, error) }, e Event, viewerID int64) {
	youFlag := "false"
	if e.userID == viewerID {
		youFlag = "true"
	}
	fmt.Fprintf(w,
		"data: {\"actor\":%q,\"name\":%q,\"public\":%t,\"provider\":%q,\"model\":%q,\"tokens\":%d,\"ts\":%d,\"is_you\":%s}\n\n",
		e.ActorID, e.Name, e.Public, e.Provider, e.Model, e.Tokens, e.TS, youFlag)
}
