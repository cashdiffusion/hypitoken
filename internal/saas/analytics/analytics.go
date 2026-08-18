// Package analytics implements site-wide visitor-behaviour tracking for the
// SaaS layer. Where internal/saas/growth only follows ?ref= channel visitors,
// this module captures EVERY landing-page visitor — what they did first
// (bounced / clicked Start / went to docs …), how long they stayed, the path
// they walked, and the coarse source they came from — and surfaces it under the
// admin "Growth" tab alongside the channel stats.
//
// It is deliberately self-contained: it owns its own two tables (web_sessions,
// web_events — created by SaaS migration v7) and touches the rest of the
// codebase only through a single *sql.DB handle. It imports nothing from
// internal/saas/{db,auth,billing}; wiring happens in cmd/server/main.go and
// internal/saas/adapter. Every public endpoint is fire-and-forget — a tracking
// failure can never block or surface to the visitor.
package analytics

import (
	"database/sql"
	"net/url"
	"strings"
)

// Service is the analytics module. Construct with New and hold a single
// instance; it is safe for concurrent use (all state lives in SQLite).
type Service struct {
	db *sql.DB
	// owned is true when Open created the handle (its own analytics.db) and
	// false when New wrapped a caller's — only the former may Close it.
	owned bool
}

// New builds the analytics service over an open SQLite handle.
func New(db *sql.DB) *Service { return &Service{db: db} }

const (
	// maxIDLen caps client-supplied visitor / session identifiers so a hostile
	// client can't bloat a row with an oversized id.
	maxIDLen = 64
	// maxNameLen caps an event name / page label / landing path, keeping the
	// table from being stuffed with arbitrary long strings.
	maxNameLen = 80
	// maxDwellMS clamps a single reported dwell time at 6 hours; beyond that the
	// figure is almost certainly a backgrounded tab or a tampered client, and
	// clamping keeps one bad sample from skewing the average. Mirrors
	// growth.maxDwellMS.
	maxDwellMS = int64(6 * 60 * 60 * 1000)
)

// sanitizeID trims and length-bounds a client-supplied identifier.
func sanitizeID(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxIDLen {
		s = s[:maxIDLen]
	}
	return s
}

// sanitizeName trims and length-bounds an event name / page label.
func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxNameLen {
		s = s[:maxNameLen]
	}
	return s
}

// searchHosts / socialHosts are substring fragments matched against the
// referrer host to bucket an acquisition source. Substring (not exact) match so
// regional variants (google.co.jp, m.facebook.com, …) fold into one bucket.
var searchHosts = []string{
	"google.", "bing.", "baidu.", "duckduckgo.", "yahoo.", "yandex.",
	"sogou.", "so.com", "ecosia.", "qwant.",
}

var socialHosts = []string{
	"twitter.com", "x.com", "t.co", "reddit.com", "facebook.com", "fb.com",
	"linkedin.com", "youtube.com", "t.me", "telegram", "weibo.", "zhihu.com",
	"bilibili.com", "github.com", "news.ycombinator.com", "instagram.com",
	"tiktok.com", "douyin.", "discord.", "juejin.", "csdn.net",
}

func matchesAny(host string, fragments []string) bool {
	for _, f := range fragments {
		if strings.Contains(host, f) {
			return true
		}
	}
	return false
}

// classifySource buckets a raw document.referrer into a coarse acquisition
// channel and returns (source, host). selfHost is the request's own Host so a
// same-site navigation counts as "internal" rather than an external referral.
// Only meaningful for the landing event; later events reuse the stored source.
func classifySource(referrer, selfHost string) (source, host string) {
	referrer = strings.TrimSpace(referrer)
	if referrer == "" {
		return "direct", ""
	}
	u, err := url.Parse(referrer)
	if err != nil || u.Host == "" {
		return "direct", ""
	}
	host = strings.ToLower(u.Hostname())

	self := strings.ToLower(strings.TrimSpace(selfHost))
	if i := strings.IndexByte(self, ':'); i >= 0 {
		self = self[:i]
	}
	if self != "" && (host == self || strings.HasSuffix(host, "."+self)) {
		return "internal", host
	}
	switch {
	case matchesAny(host, searchHosts):
		return "search", host
	case matchesAny(host, socialHosts):
		return "social", host
	default:
		return "referral", host
	}
}
