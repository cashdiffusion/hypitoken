package auth

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// NormalizeGroup canonicalizes a group identifier. Empty string and the
// literal "public" (case-insensitive) both mean the public/default pool.
// The reserved built-in "new" group is always lowercased so any casing
// ("NEW", "New") collapses to the same tier. All other values are trimmed
// and preserved case-sensitively.
func NormalizeGroup(s string) string {
	g := strings.TrimSpace(s)
	if g == "" {
		return ""
	}
	if strings.EqualFold(g, "public") {
		return ""
	}
	if strings.EqualFold(g, "new") {
		return "new"
	}
	return g
}

// Kind distinguishes OAuth credentials (concurrency-limited) from API keys
// (unlimited; used as fallback).
type Kind int

const (
	KindOAuth Kind = iota
	KindAPIKey
)

// Auth is a single upstream credential.
// For OAuth: AccessToken/RefreshToken/ExpiresAt are managed by the refresher.
// For APIKey: only AccessToken (the literal key) is used.
type Auth struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex // serializes OAuth refresh calls; prevents concurrent burns of a rotating refresh_token

	ID       string // stable identifier (OAuth: file basename; APIKey: "apikey:<label-or-prefix>")
	Kind     Kind
	Provider string // "anthropic" | "openai" — drives routing + per-provider token endpoints
	Label    string
	Email    string

	// Credentials
	AccessToken  string
	RefreshToken string // OAuth only
	ExpiresAt    time.Time

	// Codex (OpenAI) OAuth specifics. IDToken carries the ChatGPT account
	// claims; AccountID + PlanType drive upstream request headers and
	// per-plan model visibility. Unused for Anthropic auths.
	IDToken   string
	AccountID string
	PlanType  string

	// Anthropic OAuth account/org UUIDs returned by the token-exchange
	// response. Used by the body mimicry layer to populate
	// metadata.user_id.account_uuid so requests look identical to the real
	// Claude Code CLI's. Empty when not yet captured (legacy credentials
	// saved before this field existed); body mimicry then falls back to
	// deriving a stable per-account anchor from Email or ID.
	AccountUUID      string
	OrganizationUUID string

	// Subscription / rate-limit tier captured from the
	// /api/claude_cli/bootstrap response (oauth_account.organization_type
	// and organization_rate_limit_tier). Used by the GrowthBook sidecar
	// (buildGrowthBookBody) to send authentic per-account experiment
	// attributes instead of the previous hardcoded "max" defaults — a
	// hardcoded value that didn't match the real subscription is itself
	// a fingerprint signal. Empty when not yet captured (the very first
	// bootstrap pass falls back to "max" / "default_claude_max_20x");
	// persisted to the credential file once known so subsequent process
	// starts have it without waiting for another bootstrap.
	OrganizationType          string // e.g. "claude_max", "claude_pro", "claude_team"
	OrganizationRateLimitTier string // e.g. "default_claude_max_20x"

	// Routing
	ProxyURL      string // per-credential upstream proxy (empty = direct/use default)
	BaseURL       string // per-credential upstream base URL override (API-key only; empty = config.AnthropicBaseURL)
	MaxConcurrent int    // OAuth: max client sessions; 0 = unlimited. APIKey: ignored.

	// Group gates which client tokens may be served by this credential.
	// Empty string = public pool (usable by anyone). Any other value means
	// the credential is restricted to client tokens whose Group matches, with
	// public acting as a fallback when the group's credentials are exhausted.
	Group string

	// ModelMap (API-key only) maps client-facing model names to upstream
	// model names. When non-nil and non-empty:
	//   - this credential will only be picked for requests whose model
	//     appears as a key in the map (acts as a routing filter)
	//   - the request body's "model" field is rewritten to the mapped value
	//     before being sent upstream (the empty string means "accept this
	//     model name but don't rewrite")
	// Nil/empty map = wildcard (no filtering, no rewriting). OAuth credentials
	// ignore this field.
	ModelMap map[string]string

	// Source file for OAuth (empty for APIKey)
	FilePath string

	// Health
	Disabled            bool
	QuotaExceededAt     time.Time // zero = not flagged
	QuotaResetAt        time.Time // when to try again (may be zero = manual reset)
	LastFailure         time.Time
	LastFailureReason   string
	LastSuccess         time.Time // set on every <400 upstream response
	ConsecutiveFailures int       // reset on success; drives auto hard-fail
	Consecutive429s     int       // reset on success; drives 429-specific hard-fail (suspected stealth ban)
	HardFailureAt       time.Time // sticky unhealthy; cleared only by ClearFailure
	HardFailureReason   string

	// Client-initiated cancellations (ctrl-C, connection close). Tracked
	// for admin visibility only — does NOT affect IsHealthy / cooldown /
	// consecutive-failure counters, since the credential itself is fine.
	LastClientCancel       time.Time
	LastClientCancelReason string

	// Codex rate-limit snapshot. ChatGPT's Codex backend returns
	// x-codex-* headers on every response describing the caller's
	// rolling primary (5h) and secondary (weekly) quota windows.
	// We capture them verbatim so the admin UI can render whatever
	// fields the backend currently exposes without code changes per
	// header addition. CodexRateLimitsAt is zero = never captured.
	CodexRateLimits   map[string]string
	CodexRateLimitsAt time.Time
}

// healthGrace is how long after an isolated failure we still treat the
// credential as healthy (optimistic recovery). Hard failures and repeated
// failures bypass this.
const healthGrace = 2 * time.Minute

// hardFailureThreshold is the number of consecutive non-cooldown failures
// after which a credential is marked hard-unhealthy and must be manually
// reset from the admin panel.
const hardFailureThreshold = 5

// rateLimit429HardFailureThreshold is the number of consecutive 429
// responses after which a credential is presumed stealth-banned (Anthropic
// occasionally hides bans behind perpetual 429s rather than a clean 401/403)
// and marked hard-unhealthy. Counter resets on any successful response.
const rateLimit429HardFailureThreshold = 15

// clearExpiredQuotaLocked auto-clears the quota cooldown fields once their
// reset time has passed, so stale "quota exceeded" state never lingers in
// admin/UI/routing after the credential has actually recovered. Caller MUST
// hold a.mu write lock. Keeps behavior identical to IsQuotaExceeded's expiry
// rules: known reset → clear when reached; unknown reset → clear after 1h.
func (a *Auth) clearExpiredQuotaLocked(now time.Time) {
	if a.QuotaExceededAt.IsZero() {
		return
	}
	if a.QuotaResetAt.IsZero() {
		if now.Before(a.QuotaExceededAt.Add(time.Hour)) {
			return
		}
	} else if now.Before(a.QuotaResetAt) {
		return
	}
	a.QuotaExceededAt = time.Time{}
	a.QuotaResetAt = time.Time{}
}

func (a *Auth) Snapshot() AuthInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clearExpiredQuotaLocked(time.Now())
	var mm map[string]string
	if len(a.ModelMap) > 0 {
		mm = make(map[string]string, len(a.ModelMap))
		for k, v := range a.ModelMap {
			mm[k] = v
		}
	}
	var rl map[string]string
	if len(a.CodexRateLimits) > 0 {
		rl = make(map[string]string, len(a.CodexRateLimits))
		for k, v := range a.CodexRateLimits {
			rl[k] = v
		}
	}
	return AuthInfo{
		ID:                a.ID,
		Kind:              a.Kind,
		Provider:          a.Provider,
		Label:             a.Label,
		Email:             a.Email,
		ExpiresAt:         a.ExpiresAt,
		ProxyURL:          a.ProxyURL,
		MaxConcurrent:     a.MaxConcurrent,
		Disabled:          a.Disabled,
		QuotaExceededAt:   a.QuotaExceededAt,
		QuotaResetAt:      a.QuotaResetAt,
		FilePath:          a.FilePath,
		BaseURL:           a.BaseURL,
		Group:             a.Group,
		ModelMap:          mm,
		CodexRateLimits:   rl,
		CodexRateLimitsAt: a.CodexRateLimitsAt,
	}
}

type AuthInfo struct {
	ID                string
	Kind              Kind
	Provider          string
	Label             string
	Email             string
	ExpiresAt         time.Time
	ProxyURL          string
	MaxConcurrent     int
	Disabled          bool
	QuotaExceededAt   time.Time
	QuotaResetAt      time.Time
	FilePath          string
	BaseURL           string
	Group             string
	ModelMap          map[string]string
	CodexRateLimits   map[string]string
	CodexRateLimitsAt time.Time
}

// IsQuotaExceeded reports true if Anthropic has signalled this auth is out of
// quota and we should skip it until QuotaResetAt (or until manually cleared).
// As a side effect, auto-clears the cooldown fields once their reset time has
// passed so callers don't see stale state.
func (a *Auth) IsQuotaExceeded(now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clearExpiredQuotaLocked(now)
	return !a.QuotaExceededAt.IsZero()
}

func (a *Auth) MarkQuotaExceeded(resetAt time.Time) {
	a.mu.Lock()
	a.QuotaExceededAt = time.Now()
	a.QuotaResetAt = resetAt
	a.mu.Unlock()
}

func (a *Auth) MarkFailure(reason string) {
	a.mu.Lock()
	a.LastFailure = time.Now()
	a.LastFailureReason = reason
	a.ConsecutiveFailures++
	if a.ConsecutiveFailures >= hardFailureThreshold && a.HardFailureAt.IsZero() {
		a.HardFailureAt = a.LastFailure
		a.HardFailureReason = fmt.Sprintf("%d consecutive failures: %s", a.ConsecutiveFailures, reason)
	}
	a.mu.Unlock()
}

// MarkRateLimited records a 429 response. 429 alone is not a credential
// fault (rate limit, transient), so it does not increment the generic
// ConsecutiveFailures counter and does not set LastFailure — those drive
// the "degraded" UI state, which is too noisy for normal rate-limiting.
// Instead it bumps a dedicated 429 counter; once it crosses
// rateLimit429HardFailureThreshold, the credential is presumed stealth-
// banned and flipped to sticky hard-failure (Anthropic sometimes serves
// bans as endless 429s rather than a clean 401/403).
//
// Returns the new Consecutive429s value so callers (the pool) can pick a
// backoff length that grows with repeated hits.
func (a *Auth) MarkRateLimited(reason string) int {
	a.mu.Lock()
	a.Consecutive429s++
	n := a.Consecutive429s
	if a.Consecutive429s >= rateLimit429HardFailureThreshold && a.HardFailureAt.IsZero() {
		a.HardFailureAt = time.Now()
		a.HardFailureReason = fmt.Sprintf("%d consecutive 429s (suspected stealth ban): %s", a.Consecutive429s, reason)
		a.LastFailure = a.HardFailureAt
		a.LastFailureReason = a.HardFailureReason
	}
	a.mu.Unlock()
	return n
}

// MarkUsageLimitReached records a Claude subscription usage-limit 429 (the
// body carries "Claude AI usage limit reached|<unix-ts>"). This is the
// regular 5h/weekly quota signal and resets exactly when Anthropic says it
// will, so we set a real cooldown and explicitly do NOT touch the
// Consecutive429s counter — it would otherwise tick toward a stealth-ban
// hard-failure for an account that's actually fine.
func (a *Auth) MarkUsageLimitReached(resetAt time.Time) {
	a.mu.Lock()
	a.QuotaExceededAt = time.Now()
	a.QuotaResetAt = resetAt
	a.mu.Unlock()
}

// MarkClientCancel records that a request through this credential was
// aborted by the client (context canceled before upstream responded). This
// is surfaced to the admin panel as a non-fatal hint but never touches
// health state — the credential itself did nothing wrong.
func (a *Auth) MarkClientCancel(reason string) {
	if len(reason) > 200 {
		reason = reason[:200] + "..."
	}
	a.mu.Lock()
	a.LastClientCancel = time.Now()
	a.LastClientCancelReason = reason
	a.mu.Unlock()
}

// ClientCancelSnapshot returns the most recent client-cancel timestamp and
// reason, if any. Zero time means none recorded.
func (a *Auth) ClientCancelSnapshot() (time.Time, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.LastClientCancel, a.LastClientCancelReason
}

// MarkHardFailure flags the credential as sticky-unhealthy. The admin panel
// must manually clear it before traffic resumes. Used for obvious terminal
// signals (e.g. account disabled, upstream dead).
func (a *Auth) MarkHardFailure(reason string) {
	a.mu.Lock()
	a.HardFailureAt = time.Now()
	a.HardFailureReason = reason
	a.LastFailure = a.HardFailureAt
	a.LastFailureReason = reason
	a.mu.Unlock()
}

// MarkSuccess records that the most recent upstream request through this
// credential succeeded. Used by the admin panel to compute "healthy" status.
func (a *Auth) MarkSuccess() {
	a.mu.Lock()
	a.LastSuccess = time.Now()
	a.ConsecutiveFailures = 0
	a.Consecutive429s = 0
	a.mu.Unlock()
}

// ClearFailure wipes transient and hard failure state, returning the
// credential to "healthy". Invoked from the admin panel.
func (a *Auth) ClearFailure() {
	a.mu.Lock()
	a.LastFailure = time.Time{}
	a.LastFailureReason = ""
	a.ConsecutiveFailures = 0
	a.Consecutive429s = 0
	a.HardFailureAt = time.Time{}
	a.HardFailureReason = ""
	a.LastSuccess = time.Now()
	a.mu.Unlock()
}

// IsHealthy returns true if the credential is enabled, not in cooldown, and
// the most recent observed upstream attempt either succeeded or there has
// been no failure recorded at all. A credential that has never been used is
// considered healthy.
func (a *Auth) IsHealthy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clearExpiredQuotaLocked(time.Now())
	if a.Disabled {
		return false
	}
	if !a.HardFailureAt.IsZero() {
		return false
	}
	if !a.QuotaExceededAt.IsZero() {
		return false
	}
	if a.LastFailure.IsZero() {
		return true
	}
	if a.LastSuccess.After(a.LastFailure) {
		return true
	}
	// Optimistic recovery: a single stale failure no longer counts. Repeated
	// failures within the grace window keep the credential red.
	if a.ConsecutiveFailures < 2 && time.Since(a.LastFailure) > healthGrace {
		return true
	}
	return false
}

// HealthSnapshot returns a copy of the fields the admin panel needs to
// render health state. Auto-clears expired quota state as a side effect so
// the panel never shows a "quota exceeded" badge after the cooldown has
// elapsed.
func (a *Auth) HealthSnapshot() (healthy, hardFailure bool, reason string, consecutive int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clearExpiredQuotaLocked(time.Now())
	hardFailure = !a.HardFailureAt.IsZero()
	consecutive = a.ConsecutiveFailures
	switch {
	case hardFailure:
		reason = a.HardFailureReason
	case !a.LastFailure.IsZero() && !a.LastSuccess.After(a.LastFailure):
		reason = a.LastFailureReason
	}
	// Recompute healthy with the same logic as IsHealthy but without
	// re-acquiring the lock.
	switch {
	case a.Disabled:
		healthy = false
	case hardFailure:
		healthy = false
	case !a.QuotaExceededAt.IsZero():
		healthy = false
	case a.LastFailure.IsZero(), a.LastSuccess.After(a.LastFailure):
		healthy = true
	case a.ConsecutiveFailures < 2 && time.Since(a.LastFailure) > healthGrace:
		healthy = true
	default:
		healthy = false
	}
	return
}

// Credentials returns a snapshot of the fields needed to authenticate an
// upstream request. Safe for concurrent callers.
func (a *Auth) Credentials() (accessToken string, kind Kind) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.AccessToken, a.Kind
}

// CodexIdentity returns the Codex/OpenAI-specific identity fields
// (account_id + plan_type) under the read lock. Empty strings for
// Anthropic auths.
func (a *Auth) CodexIdentity() (accountID, planType string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.AccountID, a.PlanType
}

// CaptureCodexRateLimits extracts all x-codex-* response headers from upstream
// and stores them as the latest rate-limit snapshot. Called after every Codex
// upstream response regardless of status — a 403 or 429 also carries these
// fields and keeping the view fresh matters most right when the user is about
// to complain about a limit. Missing headers are simply ignored; we keep the
// prior snapshot intact rather than wiping it.
func (a *Auth) CaptureCodexRateLimits(h map[string][]string) {
	if len(h) == 0 {
		return
	}
	captured := make(map[string]string, 8)
	for k, vs := range h {
		if len(vs) == 0 {
			continue
		}
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-codex-") {
			captured[lk] = vs[0]
		}
	}
	if len(captured) == 0 {
		return
	}
	a.mu.Lock()
	a.CodexRateLimits = captured
	a.CodexRateLimitsAt = time.Now()
	a.mu.Unlock()
}

// IsHardFailed reports whether the credential has been flagged sticky-
// unhealthy and must be manually cleared before traffic resumes.
func (a *Auth) IsHardFailed() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return !a.HardFailureAt.IsZero()
}

func (a *Auth) ClearQuota() {
	a.mu.Lock()
	a.QuotaExceededAt = time.Time{}
	a.QuotaResetAt = time.Time{}
	a.mu.Unlock()
}

// SetDisabled toggles the disabled flag.
func (a *Auth) SetDisabled(v bool) {
	a.mu.Lock()
	a.Disabled = v
	a.mu.Unlock()
}

// SetMaxConcurrent updates the slot cap for this credential.
func (a *Auth) SetMaxConcurrent(n int) {
	if n < 0 {
		n = 0
	}
	a.mu.Lock()
	a.MaxConcurrent = n
	a.mu.Unlock()
}

// SetProxyURL updates the per-credential upstream proxy. Empty string clears it.
func (a *Auth) SetProxyURL(u string) {
	a.mu.Lock()
	a.ProxyURL = u
	a.mu.Unlock()
}

// SetBaseURL updates the per-credential upstream base URL (API-key only).
// Empty string reverts to the server-wide default.
func (a *Auth) SetBaseURL(u string) {
	a.mu.Lock()
	a.BaseURL = u
	a.mu.Unlock()
}

// SetGroup updates the credential's group. Empty string or "public" (case-
// insensitive) means the public pool.
func (a *Auth) SetGroup(g string) {
	a.mu.Lock()
	a.Group = NormalizeGroup(g)
	a.mu.Unlock()
}

// GroupName returns the credential's group under the lock.
func (a *Auth) GroupName() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Group
}

// SetModelMap replaces the credential's client→upstream model map. Empty/nil
// map clears it (credential becomes wildcard again). API-key only — calling
// on an OAuth credential stores the value but it is ignored at routing time.
func (a *Auth) SetModelMap(m map[string]string) {
	cleaned := make(map[string]string, len(m))
	for k, v := range m {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		cleaned[k] = strings.TrimSpace(v)
	}
	a.mu.Lock()
	if len(cleaned) == 0 {
		a.ModelMap = nil
	} else {
		a.ModelMap = cleaned
	}
	a.mu.Unlock()
}

// ResolveUpstreamModel returns the upstream model name to send for a given
// client-facing model. ok=false means this credential does not accept the
// model (caller should skip it during routing). When ok=true, upstream is
// the model name to put in the request body — empty string means "send the
// client's model name unchanged".
//
// Wildcard credentials (nil/empty ModelMap) always return (clientModel, true).
func (a *Auth) ResolveUpstreamModel(clientModel string) (upstream string, ok bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.ModelMap) == 0 {
		return clientModel, true
	}
	mapped, exists := a.ModelMap[clientModel]
	if !exists {
		return "", false
	}
	if mapped == "" {
		return clientModel, true
	}
	return mapped, true
}

// AcceptsModel reports whether this credential is eligible to serve a request
// for the given client-facing model. Used by the pool selector. Wildcard
// (empty ModelMap) accepts everything.
func (a *Auth) AcceptsModel(clientModel string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.ModelMap) == 0 {
		return true
	}
	_, ok := a.ModelMap[clientModel]
	return ok
}
