package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wjsoj/CPA-Claude/internal/pricing"
	"github.com/wjsoj/CPA-Claude/internal/saas"
	"gopkg.in/yaml.v3"
)

type APIKey struct {
	Key      string            `yaml:"key"`
	Provider string            `yaml:"provider,omitempty"` // "anthropic" | "openai"; empty = anthropic (legacy)
	ProxyURL string            `yaml:"proxy_url,omitempty"`
	Label    string            `yaml:"label,omitempty"`
	BaseURL  string            `yaml:"base_url,omitempty"`
	Group    string            `yaml:"group,omitempty"`
	// ModelMap routes/rewrites client-facing model names to upstream model
	// names. See auth.Auth.ModelMap. Non-empty map turns this key into a
	// model-restricted credential. Empty = wildcard.
	ModelMap map[string]string `yaml:"model_map,omitempty"`
}

// EndpointConfig selects the listening host/port for one provider-scoped
// HTTP endpoint. An endpoint is considered live when Port > 0 and !Disabled
// — this lets users toggle Claude or Codex off without having to remove the
// whole section.
type EndpointConfig struct {
	Host     string `yaml:"host,omitempty"`
	Port     int    `yaml:"port,omitempty"`
	Disabled bool   `yaml:"disabled,omitempty"`
}

// IsEnabled reports whether this endpoint should be bound on startup.
func (e EndpointConfig) IsEnabled() bool { return !e.Disabled && e.Port > 0 }

// EndpointsConfig groups the per-provider endpoint configs. Both endpoints
// share the same upstream credential pool, client token store, usage store,
// and request log. They differ only in the routes they expose and the
// credential subset they route to.
type EndpointsConfig struct {
	Claude EndpointConfig `yaml:"claude"`
	Codex  EndpointConfig `yaml:"codex"`
}

type Config struct {
	LogLevel string `yaml:"log_level"`

	// Endpoints configures per-provider HTTP listeners. See the struct doc
	// on EndpointsConfig. Admin panel + public status page are served on
	// whichever endpoint is designated primary (claude if enabled, else
	// codex, else startup fails).
	Endpoints EndpointsConfig `yaml:"endpoints"`

	// Directory containing OAuth credential JSON files.
	AuthDir string `yaml:"auth_dir"`

	// Persistence file for usage statistics and session state.
	StateFile string `yaml:"state_file"`

	// Minutes of inactivity after which a client session releases its OAuth slot.
	ActiveWindowMinutes int `yaml:"active_window_minutes"`

	// Token required to access the management panel and APIs.
	// Empty = panel disabled. Send as X-Admin-Token header (or Authorization: Bearer).
	AdminToken string `yaml:"admin_token,omitempty"`

	// URL prefix for the management panel. Changing this from the default
	// makes trivial `/admin`-style dictionary scans miss the panel. Must
	// start with "/" and must not end with "/". Default: /mgmt-console.
	AdminPath string `yaml:"admin_path,omitempty"`

	// API-key fallback pool. No concurrency limit.
	APIKeys []APIKey `yaml:"api_keys"`

	// Default upstream proxy URL used when an OAuth file has none specified.
	DefaultProxyURL string `yaml:"default_proxy_url,omitempty"`

	// Anthropic API base URL (override for testing).
	AnthropicBaseURL string `yaml:"anthropic_base_url,omitempty"`

	// OpenAI API base URL (override for testing; used for BYOK Codex API-key
	// routing). Defaults to https://api.openai.com/v1.
	//
	// The full upstream URL is `BaseURL + endpoint` where endpoint is
	// /responses, /chat/completions, /models, etc — without a /v1/ prefix.
	// If a third-party gateway (e.g. tcdmx) serves at the root without /v1/,
	// configure the credential with BaseURL=https://gateway.example and
	// it'll receive /responses directly. This makes BaseURL authoritative
	// for whether /v1/ ends up in the upstream URL.
	OpenAIBaseURL string `yaml:"openai_base_url,omitempty"`

	// Codex OAuth-authenticated requests hit the ChatGPT backend, not the
	// public OpenAI API. This base URL is here so installations behind
	// vendor relays can override it; normally unchanged.
	ChatGPTBackendBaseURL string `yaml:"chatgpt_backend_base_url,omitempty"`

	// If true, OAuth/API-key refresh+request uses utls Chrome fingerprint.
	UseUTLS bool `yaml:"use_utls"`

	// Directory for per-request JSONL logs (one file per day:
	// requests-YYYY-MM-DD.jsonl). Empty = disabled.
	LogDir string `yaml:"log_dir,omitempty"`

	// Default maximum concurrent in-flight requests per client token.
	// 0 = unlimited. Per-token overrides take precedence.
	ClientMaxConcurrent int `yaml:"client_max_concurrent"`

	// Default sliding-window requests-per-minute cap per client token.
	// 0 = unlimited. Per-token overrides take precedence.
	ClientRPM int `yaml:"client_rpm"`

	// Days to retain rotated request logs. 0 = disable GC (keep forever).
	LogRetentionDays int `yaml:"log_retention_days,omitempty"`

	// Pricing overrides (optional). Built-in defaults cover claude-haiku-4-5,
	// claude-opus-4-6, and claude-sonnet-4-6.
	Pricing pricing.Config `yaml:"pricing"`

	// SaaS multi-tenant layer (commercial mode). Disabled by default; the
	// proxy behaves exactly like the OSS build when SaaS.Enabled is false.
	SaaS saas.Config `yaml:"saas"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(cfg, path)
	return cfg, nil
}

func applyDefaults(c *Config, path string) {
	if c.Endpoints.Claude.Port == 0 {
		c.Endpoints.Claude.Port = 8317
	}
	if c.Endpoints.Claude.Host == "" {
		c.Endpoints.Claude.Host = "0.0.0.0"
	}
	if c.Endpoints.Codex.Port == 0 {
		// Codex endpoint defaults to configured-but-disabled so merely
		// upgrading the server binary doesn't flip on an empty listener.
		c.Endpoints.Codex.Port = 8318
		c.Endpoints.Codex.Disabled = true
	}
	if c.Endpoints.Codex.Host == "" {
		c.Endpoints.Codex.Host = "0.0.0.0"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.ActiveWindowMinutes == 0 {
		c.ActiveWindowMinutes = 5
	}
	if c.ClientMaxConcurrent == 0 {
		c.ClientMaxConcurrent = 15
	}
	if c.ClientRPM == 0 {
		c.ClientRPM = 60
	}
	if c.AnthropicBaseURL == "" {
		c.AnthropicBaseURL = "https://api.anthropic.com"
	}
	if c.OpenAIBaseURL == "" {
		c.OpenAIBaseURL = "https://api.openai.com/v1"
	}
	if c.ChatGPTBackendBaseURL == "" {
		c.ChatGPTBackendBaseURL = "https://chatgpt.com/backend-api"
	}
	dir := filepath.Dir(path)
	if c.AuthDir == "" {
		c.AuthDir = filepath.Join(dir, "auths")
	} else if !filepath.IsAbs(c.AuthDir) {
		c.AuthDir = filepath.Join(dir, c.AuthDir)
	}
	if c.StateFile == "" {
		c.StateFile = filepath.Join(dir, "state.json")
	} else if !filepath.IsAbs(c.StateFile) {
		c.StateFile = filepath.Join(dir, c.StateFile)
	}
	if c.LogDir != "" && !filepath.IsAbs(c.LogDir) {
		c.LogDir = filepath.Join(dir, c.LogDir)
	}
	if c.LogRetentionDays == 0 {
		c.LogRetentionDays = 90
	}
	p := strings.TrimSpace(c.AdminPath)
	if p == "" {
		p = "/mgmt-console"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.TrimRight(p, "/")
	if p == "" {
		p = "/mgmt-console"
	}
	c.AdminPath = p
	c.SaaS.ApplyDefaults(filepath.Dir(path))
}
