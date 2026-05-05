// Package saas implements the multi-tenant commercial layer on top of the
// existing CPA-Claude proxy: user accounts, per-user API tokens, USD wallet
// billed per request, CNY top-up via Alipay, and a fresh frontend.
//
// When the Config.Enabled flag is false the package is dormant — main.go
// skips wiring its routes and the proxy behaves exactly like the OSS build.
package saas

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is the YAML shape of the `saas` block. Marshaled by config.Config.
type Config struct {
	Enabled bool `yaml:"enabled"`

	// DBPath is where the SQLite file lives. Relative paths resolve against
	// the dir of the loaded config file.
	DBPath string `yaml:"db_path"`

	// JWTSecret is the HS256 signing key. If empty on first start, a 32-byte
	// random secret is generated and persisted to <DBPath>.jwt_secret (mode
	// 0600) so subsequent starts reuse it.
	JWTSecret    string        `yaml:"jwt_secret"`
	JWTTTL       time.Duration `yaml:"jwt_ttl"`
	JWTRefreshTTL time.Duration `yaml:"jwt_refresh_ttl"`

	// Site identity (used in emails and the frontend).
	SiteName string `yaml:"site_name"`
	SiteURL  string `yaml:"site_url"`

	// AdminEmail / AdminPassword bootstrap a default admin user on first
	// start when the users table is empty. After bootstrap they are
	// ignored — change role/password through the panel.
	AdminEmail    string `yaml:"admin_email"`
	AdminPassword string `yaml:"admin_password"`

	// FreeRegister: if false, registration is disabled (operator pre-creates
	// users). Default true.
	FreeRegister *bool `yaml:"free_register"`

	// SMTP outbound mail. If Host is empty, mailer logs to stderr and
	// (in dev) prints verification codes inline.
	SMTP SMTPConfig `yaml:"smtp"`

	// Alipay gateway. If AppID is empty, the mock gateway is used: orders
	// auto-confirm 2 seconds after creation. Useful for local dev.
	Alipay AlipayConfig `yaml:"alipay"`

	// Exchange rate source. Empty = built-in fawazahmed0 currency-api.
	ExchangeRateURL string  `yaml:"exchange_rate_url"`
	FallbackCNYPerUSD float64 `yaml:"fallback_cny_per_usd"`

	// Default billing rates per provider (1r = X USD of official compute).
	// Formula: bill = official_cost_usd / rate. Higher rate = cheaper for user.
	// Per-credential billing_rate overrides these defaults when set.
	DefaultClaudeBillingRate float64 `yaml:"default_claude_billing_rate"`
	DefaultCodexBillingRate  float64 `yaml:"default_codex_billing_rate"`

	// HealthCheck cadence for API-key credentials.
	HealthCheckInterval time.Duration `yaml:"health_check_interval"`
}

type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
	UseTLS   bool   `yaml:"use_tls"`
}

type AlipayConfig struct {
	AppID            string `yaml:"app_id"`
	PrivateKey       string `yaml:"private_key"`         // PEM (multiline) or @path/to/file
	AlipayPublicKey  string `yaml:"alipay_public_key"`   // PEM or @path
	IsProduction     bool   `yaml:"is_production"`
	NotifyURL        string `yaml:"notify_url"`          // public webhook URL
	ReturnURL        string `yaml:"return_url"`          // browser redirect after pay
}

// ApplyDefaults fills in zero-value fields with sensible defaults. configDir
// is the directory containing config.yaml; it is used to resolve relative
// paths.
func (c *Config) ApplyDefaults(configDir string) {
	if c.DBPath == "" {
		c.DBPath = filepath.Join(configDir, "saas.db")
	} else if !filepath.IsAbs(c.DBPath) {
		c.DBPath = filepath.Join(configDir, c.DBPath)
	}
	if c.JWTTTL == 0 {
		c.JWTTTL = 24 * time.Hour
	}
	if c.JWTRefreshTTL == 0 {
		c.JWTRefreshTTL = 14 * 24 * time.Hour
	}
	if c.SiteName == "" {
		c.SiteName = "HypiToken"
	}
	if c.FreeRegister == nil {
		t := true
		c.FreeRegister = &t
	}
	if c.FallbackCNYPerUSD <= 0 {
		c.FallbackCNYPerUSD = 7.2
	}
	if c.DefaultClaudeBillingRate <= 0 {
		c.DefaultClaudeBillingRate = 0.5
	}
	if c.DefaultCodexBillingRate <= 0 {
		c.DefaultCodexBillingRate = 3.0
	}
	if c.HealthCheckInterval == 0 {
		c.HealthCheckInterval = 30 * time.Minute
	}
	if c.SMTP.Port == 0 {
		c.SMTP.Port = 587
	}
}

// EnsureJWTSecret loads or generates the HS256 signing key. Generated keys
// are persisted next to the SQLite DB so restarts don't invalidate sessions.
func (c *Config) EnsureJWTSecret() error {
	if strings.TrimSpace(c.JWTSecret) != "" {
		return nil
	}
	keyPath := c.DBPath + ".jwt_secret"
	data, err := os.ReadFile(keyPath)
	if err == nil {
		c.JWTSecret = strings.TrimSpace(string(data))
		if c.JWTSecret != "" {
			return nil
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	c.JWTSecret = hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(keyPath, []byte(c.JWTSecret), 0o600)
}
