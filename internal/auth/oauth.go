package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Anthropic OAuth constants. Mirror what `claude-cli/2.1.126` actually
// hits — the token endpoint moved off `api.anthropic.com` to a dedicated
// `platform.claude.com` host in late 2025. The client_id is the public
// OAuth application UUID for "Claude Code" (matches the `application.uuid`
// field returned by /api/oauth/profile).
const (
	anthropicTokenURL = "https://platform.claude.com/v1/oauth/token"
	anthropicClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

	// User-Agent sent by real CC for the token-exchange + refresh requests.
	// (Most other CC traffic uses claude-cli or claude-code or Bun, but
	// these two specific axios calls use this exact string.)
	anthropicOAuthUA = "axios/1.13.6"
)

// fileFormat is the JSON layout written by `claude setup-token` / our own
// login flow. We accept extra keys and preserve them on save.
type fileFormat struct {
	Type          string         `json:"type"`
	AccessToken   string         `json:"access_token"`
	RefreshToken  string         `json:"refresh_token"`
	Email         string         `json:"email,omitempty"`
	Expire        string         `json:"expired,omitempty"` // RFC3339 string
	ExpiresAt     int64          `json:"expires_at,omitempty"`
	ProxyURL      string         `json:"proxy_url,omitempty"`
	MaxConcurrent int            `json:"max_concurrent,omitempty"`
	Disabled      bool           `json:"disabled,omitempty"`
	Label         string         `json:"label,omitempty"`
	Extra         map[string]any `json:"-"`
}

func parseFile(path string, data []byte) (*Auth, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	t, _ := raw["type"].(string)
	kindStr := strings.ToLower(strings.TrimSpace(t))
	// The `type` field encodes both provider and credential kind for legacy
	// compatibility. New file layouts also carry a standalone `provider`
	// field — we prefer that when present, falling back to inference from
	// `type`. Kind inference is purely on the `type` token.
	provHint, _ := raw["provider"].(string)
	provider := NormalizeProvider(provHint)
	switch kindStr {
	case "claude":
		if provHint == "" {
			provider = ProviderAnthropic
		}
		// OAuth credential (fall through to existing parse path).
	case "codex", "openai", "chatgpt":
		if provHint == "" {
			provider = ProviderOpenAI
		}
		return parseCodexOAuthFile(path, raw, provider)
	case "apikey", "api_key", "anthropic_api_key":
		if provHint == "" {
			provider = ProviderAnthropic
		}
		return parseAPIKeyFile(path, raw, provider)
	case "openai_api_key", "codex_api_key":
		if provHint == "" {
			provider = ProviderOpenAI
		}
		return parseAPIKeyFile(path, raw, provider)
	default:
		return nil, fmt.Errorf("unsupported type %q (expected claude/codex/apikey)", t)
	}
	access, _ := raw["access_token"].(string)
	refresh, _ := raw["refresh_token"].(string)
	if access == "" && refresh == "" {
		return nil, fmt.Errorf("missing access_token and refresh_token")
	}
	email, _ := raw["email"].(string)
	label, _ := raw["label"].(string)
	if label == "" {
		label = email
	}
	if label == "" {
		label = filepath.Base(path)
	}
	disabled, _ := raw["disabled"].(bool)
	proxyURL, _ := raw["proxy_url"].(string)
	group, _ := raw["group"].(string)
	maxConc := 0
	if v, ok := raw["max_concurrent"].(float64); ok {
		maxConc = int(v)
	}

	exp := time.Time{}
	if s, ok := raw["expired"].(string); ok && s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			exp = t
		}
	}
	if exp.IsZero() {
		if v, ok := raw["expires_at"].(float64); ok {
			exp = time.Unix(int64(v), 0)
		}
	}

	accountUUID, _ := raw["account_uuid"].(string)
	orgUUID, _ := raw["organization_uuid"].(string)
	orgType, _ := raw["organization_type"].(string)
	orgRateLimitTier, _ := raw["organization_rate_limit_tier"].(string)
	a := &Auth{
		ID:                        filepath.Base(path),
		Kind:                      KindOAuth,
		Provider:                  provider,
		Label:                     label,
		Email:                     email,
		AccessToken:               access,
		RefreshToken:              refresh,
		ExpiresAt:                 exp,
		ProxyURL:                  proxyURL,
		MaxConcurrent:             maxConc,
		FilePath:                  path,
		Disabled:                  disabled,
		Group:                     NormalizeGroup(group),
		AccountUUID:               accountUUID,
		OrganizationUUID:          orgUUID,
		OrganizationType:          orgType,
		OrganizationRateLimitTier: orgRateLimitTier,
	}
	return a, nil
}

// AccountKey returns the most stable per-account anchor available on this
// credential, in priority order: AccountUUID (real value from the OAuth
// token exchange) → Email → ID. Used by body mimicry to derive a device_id
// that is constant for all requests routed through the same account, even
// across credential file reissues, so multi-user routing onto a single
// account presents as one device with multiple concurrent CC sessions.
func (a *Auth) AccountKey() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if s := strings.TrimSpace(a.AccountUUID); s != "" {
		return s
	}
	if s := strings.TrimSpace(a.Email); s != "" {
		return s
	}
	return a.ID
}

// AccountUUIDValue returns the OAuth-issued account UUID if known, else "".
func (a *Auth) AccountUUIDValue() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.AccountUUID
}

func parseAPIKeyFile(path string, raw map[string]any, provider string) (*Auth, error) {
	apiKey, _ := raw["api_key"].(string)
	if apiKey == "" {
		// Tolerate "key" / "access_token" spellings.
		if s, ok := raw["key"].(string); ok {
			apiKey = s
		} else if s, ok := raw["access_token"].(string); ok {
			apiKey = s
		}
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("missing api_key")
	}
	label, _ := raw["label"].(string)
	if label == "" {
		label = filepath.Base(path)
	}
	disabled, _ := raw["disabled"].(bool)
	proxyURL, _ := raw["proxy_url"].(string)
	baseURL, _ := raw["base_url"].(string)
	group, _ := raw["group"].(string)
	modelMap := parseModelMap(raw["model_map"])
	return &Auth{
		ID:          filepath.Base(path),
		Kind:        KindAPIKey,
		Provider:    provider,
		Label:       label,
		AccessToken: apiKey,
		ProxyURL:    proxyURL,
		BaseURL:     baseURL,
		FilePath:    path,
		Disabled:    disabled,
		Group:       NormalizeGroup(group),
		ModelMap:    modelMap,
	}, nil
}

// parseCodexOAuthFile parses a Codex (OpenAI/ChatGPT) OAuth credential file.
// Layout mirrors Claude OAuth but carries id_token + account_id + plan_type
// which Codex upstream requests depend on (session headers and per-plan
// model visibility). Expiry format differs from Claude — Codex writes
// "expired" as RFC3339 (same as Claude) for simplicity.
func parseCodexOAuthFile(path string, raw map[string]any, provider string) (*Auth, error) {
	access, _ := raw["access_token"].(string)
	refresh, _ := raw["refresh_token"].(string)
	if access == "" && refresh == "" {
		return nil, fmt.Errorf("missing access_token and refresh_token")
	}
	email, _ := raw["email"].(string)
	label, _ := raw["label"].(string)
	if label == "" {
		label = email
	}
	if label == "" {
		label = filepath.Base(path)
	}
	disabled, _ := raw["disabled"].(bool)
	proxyURL, _ := raw["proxy_url"].(string)
	group, _ := raw["group"].(string)
	idToken, _ := raw["id_token"].(string)
	accountID, _ := raw["account_id"].(string)
	planType, _ := raw["plan_type"].(string)
	maxConc := 0
	if v, ok := raw["max_concurrent"].(float64); ok {
		maxConc = int(v)
	}
	exp := time.Time{}
	if s, ok := raw["expired"].(string); ok && s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			exp = t
		}
	}
	if exp.IsZero() {
		if v, ok := raw["expires_at"].(float64); ok {
			exp = time.Unix(int64(v), 0)
		}
	}
	return &Auth{
		ID:            filepath.Base(path),
		Kind:          KindOAuth,
		Provider:      provider,
		Label:         label,
		Email:         email,
		AccessToken:   access,
		RefreshToken:  refresh,
		IDToken:       idToken,
		AccountID:     accountID,
		PlanType:      planType,
		ExpiresAt:     exp,
		ProxyURL:      proxyURL,
		MaxConcurrent: maxConc,
		FilePath:      path,
		Disabled:      disabled,
		Group:         NormalizeGroup(group),
	}, nil
}

// parseModelMap normalizes the model_map entry from a parsed JSON object into
// a Go map[string]string. Accepts a JSON object {"a":"b"} (canonical form).
// Returns nil when the input is missing/empty/wrong shape.
func parseModelMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, raw := range m {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		s, _ := raw.(string)
		out[k] = strings.TrimSpace(s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// LoadAuthDir reads every *.json under dir and splits the parsed auths into
// OAuth and API-key slices.
func LoadAuthDir(dir string) (oauths, apikeys []*Auth, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, errRead := os.ReadFile(full)
		if errRead != nil {
			log.Warnf("auth: read %s: %v", full, errRead)
			continue
		}
		a, errParse := parseFile(full, data)
		if errParse != nil {
			log.Warnf("auth: parse %s: %v", full, errParse)
			continue
		}
		if a.Kind == KindAPIKey {
			apikeys = append(apikeys, a)
		} else {
			oauths = append(oauths, a)
		}
	}
	return oauths, apikeys, nil
}

var saveMu sync.Mutex

// saveAuth atomically rewrites the OAuth file with fresh tokens plus
// admin-editable fields (disabled, max_concurrent, proxy_url), preserving
// any extra keys from the original file.
func saveAuth(a *Auth) error {
	saveMu.Lock()
	defer saveMu.Unlock()
	if a.FilePath == "" {
		return nil
	}
	var raw map[string]any
	if data, err := os.ReadFile(a.FilePath); err == nil {
		_ = json.Unmarshal(data, &raw)
	}
	if raw == nil {
		raw = make(map[string]any)
	}
	a.mu.RLock()
	provider := NormalizeProvider(a.Provider)
	raw["provider"] = provider
	if a.Kind == KindAPIKey {
		if provider == ProviderOpenAI {
			raw["type"] = "openai_api_key"
		} else {
			raw["type"] = "apikey"
		}
		raw["api_key"] = a.AccessToken
		if a.BaseURL != "" {
			raw["base_url"] = a.BaseURL
		} else {
			delete(raw, "base_url")
		}
		if len(a.ModelMap) > 0 {
			mm := make(map[string]any, len(a.ModelMap))
			for k, v := range a.ModelMap {
				mm[k] = v
			}
			raw["model_map"] = mm
		} else {
			delete(raw, "model_map")
		}
		// Clear OAuth-only keys if the file was converted.
		delete(raw, "refresh_token")
		delete(raw, "access_token")
		delete(raw, "expired")
		delete(raw, "id_token")
		delete(raw, "account_id")
		delete(raw, "plan_type")
		delete(raw, "last_refresh")
		delete(raw, "max_concurrent")
	} else {
		if provider == ProviderOpenAI {
			raw["type"] = "codex"
		} else {
			raw["type"] = "claude"
		}
		raw["access_token"] = a.AccessToken
		raw["refresh_token"] = a.RefreshToken
		if !a.ExpiresAt.IsZero() {
			raw["expired"] = a.ExpiresAt.UTC().Format(time.RFC3339)
		}
		raw["max_concurrent"] = a.MaxConcurrent
		// Codex-specific extras (empty for Claude, naturally omitted).
		if a.IDToken != "" {
			raw["id_token"] = a.IDToken
		} else {
			delete(raw, "id_token")
		}
		if a.AccountID != "" {
			raw["account_id"] = a.AccountID
		} else {
			delete(raw, "account_id")
		}
		if a.PlanType != "" {
			raw["plan_type"] = a.PlanType
		} else {
			delete(raw, "plan_type")
		}
		if a.OrganizationType != "" {
			raw["organization_type"] = a.OrganizationType
		} else {
			delete(raw, "organization_type")
		}
		if a.OrganizationRateLimitTier != "" {
			raw["organization_rate_limit_tier"] = a.OrganizationRateLimitTier
		} else {
			delete(raw, "organization_rate_limit_tier")
		}
	}
	raw["disabled"] = a.Disabled
	if a.ProxyURL != "" {
		raw["proxy_url"] = a.ProxyURL
	} else {
		delete(raw, "proxy_url")
	}
	if a.Label != "" {
		raw["label"] = a.Label
	}
	if a.Group != "" {
		raw["group"] = a.Group
	} else {
		delete(raw, "group")
	}
	// Drop legacy per-credential billing_rate from any old credential file
	// — billing now lives entirely on the pricing-group multiplier.
	delete(raw, "billing_rate")
	a.mu.RUnlock()
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.FilePath + ".tmp"
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, a.FilePath)
}

// Persist writes the current admin-editable fields of the auth back to disk.
func (a *Auth) Persist() error { return saveAuth(a) }

// UpdateSubscriptionInfo records the subscription tier captured from a
// /api/claude_cli/bootstrap response so future GrowthBook calls can
// advertise the real per-account attributes instead of the hardcoded
// "max" defaults. No-op (and no disk write) when both inputs are empty
// or already match the cached values. Persists on change so the value
// survives process restart and is available before the next bootstrap.
func (a *Auth) UpdateSubscriptionInfo(orgType, rateLimitTier string) error {
	a.mu.Lock()
	changed := false
	if orgType != "" && a.OrganizationType != orgType {
		a.OrganizationType = orgType
		changed = true
	}
	if rateLimitTier != "" && a.OrganizationRateLimitTier != rateLimitTier {
		a.OrganizationRateLimitTier = rateLimitTier
		changed = true
	}
	a.mu.Unlock()
	if !changed {
		return nil
	}
	return saveAuth(a)
}

// ParseFile is the exported variant of parseFile for admin upload handlers.
func ParseFile(path string, data []byte) (*Auth, error) { return parseFile(path, data) }

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Account      struct {
		EmailAddress string `json:"email_address"`
	} `json:"account"`
}

// needsRefresh reports whether the OAuth token is missing or within `leeway`
// of expiry. Returns false if there is no refresh token to use.
func (a *Auth) needsRefresh(leeway time.Duration) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.RefreshToken == "" {
		return false
	}
	if a.ExpiresAt.IsZero() {
		return true
	}
	return time.Until(a.ExpiresAt) < leeway
}

// EnsureFresh refreshes the access token if it's within `leeway` of expiry.
// The effective leeway is max(leeway, MinRefreshLeeway()) — providers with
// long-lived tokens (e.g. Codex at ~30 days) want to refresh days early so a
// brief upstream outage near expiry doesn't leave zero recovery window.
// Concurrent callers are deduplicated via a per-auth refresh mutex so the
// rotating refresh_token is never burned by parallel exchanges.
func (a *Auth) EnsureFresh(ctx context.Context, leeway time.Duration, useUTLS bool) error {
	if min := a.MinRefreshLeeway(); min > leeway {
		leeway = min
	}
	if !a.needsRefresh(leeway) {
		return nil
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	if !a.needsRefresh(leeway) {
		return nil
	}
	return a.doRefreshLocked(ctx, useUTLS)
}

// MinRefreshLeeway returns the per-provider minimum refresh lead time.
// Anthropic access tokens live ~8 hours — 5 minutes of lead is fine.
// OpenAI / Codex access tokens live ~30 days — refresh 5 days ahead to
// match the Codex CLI's RefreshLead, so a transient outage near expiry
// has a 5-day window to recover before the token actually dies.
func (a *Auth) MinRefreshLeeway() time.Duration {
	switch NormalizeProvider(a.Provider) {
	case ProviderOpenAI:
		return 5 * 24 * time.Hour
	default:
		return 5 * time.Minute
	}
}

// doRefreshLocked performs the HTTP exchange. Caller must hold a.refreshMu.
// Dispatches to the per-provider refresh implementation. The Anthropic path
// (default) lives inline below; Codex has its own exchange shape and token
// URL handled in codex_refresh.go.
func (a *Auth) doRefreshLocked(ctx context.Context, useUTLS bool) error {
	switch NormalizeProvider(a.Provider) {
	case ProviderOpenAI:
		return a.refreshCodexLocked(ctx, useUTLS)
	default:
		return a.refreshAnthropicLocked(ctx, useUTLS)
	}
}

// refreshAnthropicLocked refreshes a Claude OAuth access token.
func (a *Auth) refreshAnthropicLocked(ctx context.Context, useUTLS bool) error {
	a.mu.RLock()
	refresh := a.RefreshToken
	a.mu.RUnlock()
	if refresh == "" {
		return fmt.Errorf("no refresh token")
	}

	// Same wire shape as the initial token-exchange in login.go: ordered
	// JSON body (grant_type, refresh_token, client_id) + axios/1.13.6 UA +
	// gzip,br Accept-Encoding + Connection: close. Real CC reuses the same
	// `platform.claude.com/v1/oauth/token` endpoint for refresh.
	payload := struct {
		GrantType    string `json:"grant_type"`
		RefreshToken string `json:"refresh_token"`
		ClientID     string `json:"client_id"`
	}{
		GrantType:    "refresh_token",
		RefreshToken: refresh,
		ClientID:     anthropicClientID,
	}
	buf, _ := json.Marshal(payload)

	client := ClientFor(a.ProxyURL, useUTLS)
	resp, data, err := doAxiosOAuthRequest(ctx, client, http.MethodPost, anthropicTokenURL, buf)
	if err != nil {
		a.MarkFailure(fmt.Sprintf("refresh transport: %v", err))
		return fmt.Errorf("oauth refresh %s: %w", a.ID, err)
	}
	if resp.StatusCode != http.StatusOK {
		bodyStr := string(data)
		var errResp struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.Unmarshal(data, &errResp)
		switch {
		case resp.StatusCode == http.StatusBadRequest && errResp.Error == "invalid_grant":
			// Refresh token revoked / not found / already used. Terminal —
			// requires manual re-login. Mark hard so the picker stops handing
			// this auth out and the admin panel surfaces it.
			a.MarkHardFailure(fmt.Sprintf("refresh_token revoked (invalid_grant): %s", errResp.ErrorDescription))
		case resp.StatusCode == http.StatusUnauthorized:
			a.MarkHardFailure(fmt.Sprintf("refresh unauthorized (http 401): %s", bodyStr))
		default:
			a.MarkFailure(fmt.Sprintf("refresh http %d", resp.StatusCode))
		}
		return fmt.Errorf("oauth refresh %s: http %d: %s", a.ID, resp.StatusCode, bodyStr)
	}
	var tr refreshResponse
	if err := json.Unmarshal(data, &tr); err != nil {
		a.MarkFailure(fmt.Sprintf("refresh parse: %v", err))
		return fmt.Errorf("oauth refresh %s: parse: %w", a.ID, err)
	}
	a.mu.Lock()
	a.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		a.RefreshToken = tr.RefreshToken
	}
	if tr.ExpiresIn > 0 {
		a.ExpiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	if a.Email == "" && tr.Account.EmailAddress != "" {
		a.Email = tr.Account.EmailAddress
	}
	a.mu.Unlock()
	// Successful refresh implicitly clears any prior transient failure state
	// — the credential is demonstrably alive again.
	a.MarkSuccess()
	if err := saveAuth(a); err != nil {
		log.Warnf("auth: persist refreshed token %s: %v", a.ID, err)
	} else {
		log.Infof("auth: refreshed %s (exp=%s)", a.ID, a.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}
