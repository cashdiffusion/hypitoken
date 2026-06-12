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
	JWTSecret     string        `yaml:"jwt_secret"`
	JWTTTL        time.Duration `yaml:"jwt_ttl"`
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

	// SignupBonusUSD is the trial credit granted to every new user who did NOT
	// arrive through a marketing channel (?ref=). Channel signups get the
	// channel's own bonus instead (see internal/saas/growth). Default 1.0; set
	// to 0 to disable the default trial credit.
	SignupBonusUSD *float64 `yaml:"signup_bonus_usd"`

	// SMTP outbound mail. If Host is empty, mailer logs to stderr and
	// (in dev) prints verification codes inline.
	SMTP SMTPConfig `yaml:"smtp"`

	// Payment gateway selector. One of "alipay" (direct merchant), "zpay"
	// (易支付 aggregator), or "mock" (auto-confirms after 2s — dev only).
	// Empty defaults to whichever block is configured: zpay if zpay.pid
	// set, alipay if alipay.app_id set, else mock.
	PaymentProvider string `yaml:"payment_provider,omitempty"`

	// Alipay direct-merchant gateway. Requires real merchant onboarding.
	Alipay AlipayConfig `yaml:"alipay"`

	// Z-Pay 易支付 aggregator gateway — works for individual operators
	// without a business license.
	ZPay ZPayConfig `yaml:"zpay"`

	// Stripe gateway — runs *alongside* the QR gateway above (zpay/alipay),
	// not instead of it. When enabled it powers the embedded Payment Element
	// top-up surface (card / Alipay / WeChat Pay / crypto, all charged 1:1 in
	// USD). The QR gateway stays available as a fallback so a Stripe outage or
	// an Alipay-method restriction never takes down top-ups entirely.
	Stripe StripeConfig `yaml:"stripe"`

	// Exchange rate source. Empty = built-in fawazahmed0 currency-api.
	ExchangeRateURL   string  `yaml:"exchange_rate_url"`
	FallbackCNYPerUSD float64 `yaml:"fallback_cny_per_usd"`

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

// ZPayConfig configures the Z-Pay aggregator gateway. Key is sensitive
// — prefer the @path/to/file form so the secret stays out of the YAML
// committed to git.
type ZPayConfig struct {
	BaseURL   string `yaml:"base_url,omitempty"` // default https://zpayz.cn
	PID       string `yaml:"pid"`                // 商户ID
	Key       string `yaml:"key"`                // 商户密钥, or @/path/to/file
	NotifyURL string `yaml:"notify_url"`         // public webhook
	ReturnURL string `yaml:"return_url,omitempty"`
}

// StripeConfig configures the Stripe Payment Element top-up surface. Secret
// fields accept the @path/to/file form so they stay out of the YAML committed
// to git. Stripe charges the wallet top-up amount 1:1 in Currency (USD by
// default) — no exchange-rate coupling.
type StripeConfig struct {
	Enabled bool `yaml:"enabled"`

	// SecretKey is the sk_(test|live)_… key — backend only. @path supported.
	SecretKey string `yaml:"secret_key"`
	// PublishableKey is the pk_(test|live)_… key, handed to the browser to
	// mount the Payment Element. Public by design, but kept in config so test
	// and live keys move together.
	PublishableKey string `yaml:"publishable_key"`
	// WebhookSecret is the whsec_… signing secret for the
	// /billing/stripe/webhook endpoint. @path supported. Optional: the poll
	// path (orderStatus retrieves the PaymentIntent live) credits orders even
	// without a delivered webhook, so this can be omitted in local dev.
	WebhookSecret string `yaml:"webhook_secret"`

	// Currency is the ISO presentment currency. Default "usd" — credits the
	// USD wallet 1:1. Lowercase.
	Currency string `yaml:"currency,omitempty"`

	// PaymentMethodConfiguration optionally pins a specific
	// pmc_… payment-method configuration (the set of enabled rails: card,
	// alipay, wechat_pay, crypto). Empty = use the account's default
	// dashboard configuration via automatic_payment_methods.
	PaymentMethodConfiguration string `yaml:"payment_method_configuration,omitempty"`

	// ReturnURL is where redirect-based methods (Alipay/WeChat/crypto) send
	// the browser back after authorization. Empty = SiteURL + "/app/billing".
	ReturnURL string `yaml:"return_url,omitempty"`
}

type AlipayConfig struct {
	AppID           string `yaml:"app_id"`
	PrivateKey      string `yaml:"private_key"`       // PEM (multiline) or @path/to/file
	AlipayPublicKey string `yaml:"alipay_public_key"` // PEM or @path
	IsProduction    bool   `yaml:"is_production"`
	NotifyURL       string `yaml:"notify_url"` // public webhook URL
	ReturnURL       string `yaml:"return_url"` // browser redirect after pay
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
	if c.SignupBonusUSD == nil {
		v := 1.0
		c.SignupBonusUSD = &v
	}
	if c.FallbackCNYPerUSD <= 0 {
		c.FallbackCNYPerUSD = 7.2
	}
	if strings.TrimSpace(c.Stripe.Currency) == "" {
		c.Stripe.Currency = "usd"
	} else {
		c.Stripe.Currency = strings.ToLower(strings.TrimSpace(c.Stripe.Currency))
	}
	if c.HealthCheckInterval == 0 {
		c.HealthCheckInterval = 10 * time.Minute
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
