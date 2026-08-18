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

	// LocalSnapshotDays is how many daily on-disk snapshots to keep in
	// <dbdir>/backups. These are the local safety net; the off-host encrypted
	// backup (see the top-level `backup:` block) is the real one, so this does
	// not need a long tail. Default 14 — at ~190 MB per snapshot and growing,
	// the previous hardcoded 30 was heading for ~6 GB. Set 0 for the default.
	LocalSnapshotDays int `yaml:"local_snapshot_days,omitempty"`

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
	// channel's own bonus instead (see internal/saas/growth).
	//
	// Default 0 — the welcome-credit programme is SUSPENDED. It was farmed:
	// the 2026-08-08 incident burned ~$116 across 168 throwaway signups. The
	// key is kept so an existing production config.yaml keeps parsing.
	//
	// It is subordinate to ReferralsEnabled: an amount set here is granted only
	// while referrals_enabled is true. That matters on rollout — a deployed
	// config may already pin signup_bonus_usd to a positive value, and this
	// suspension must take effect from the binary alone, with no config edit.
	SignupBonusUSD *float64 `yaml:"signup_bonus_usd"`

	// ReferralsEnabled is the master switch for the whole invite / referral /
	// marketing-channel-attribution programme: personal invite codes, the
	// two-sided invite bonus, milestone tiers, ?ref= channel attribution and
	// its public visit/dwell beacons.
	//
	// Default FALSE — suspended after the 2026-08-08 farming incident (168
	// signups, ~$116 granted through invite fission). Off means the user-facing
	// routes are not mounted and registration grants nothing and records no
	// conversion; the packages, tables, ledger history and the admin-side
	// historical/audit routes stay intact so what was already granted can be
	// audited. Set to true to re-enable.
	ReferralsEnabled *bool `yaml:"referrals_enabled"`

	// SignupFraud tunes the signup anti-abuse check that withholds the welcome
	// bonus from a device/network that already registered.
	SignupFraud SignupFraudConfig `yaml:"signup_fraud"`

	// Invoice configures the 开票 flow: where company-name lookups go and which
	// 对公 account customers transfer to. Both have working built-in defaults —
	// set these only to override.
	Invoice InvoiceConfig `yaml:"invoice"`

	// SMTP outbound mail. If Host is empty, mailer logs to stderr and
	// (in dev) prints verification codes inline.
	SMTP SMTPConfig `yaml:"smtp"`

	// Payment gateway selector. One of "alipay" (direct merchant), "zpay"
	// (易支付 aggregator), or "mock" (auto-confirms after 2s — dev only).
	// Empty defaults to whichever block is configured: zpay if zpay.pid
	// set, alipay if alipay.app_id set, else mock — but the mock fallback is
	// hard-blocked on a public/production site (see AllowMockPayment).
	PaymentProvider string `yaml:"payment_provider,omitempty"`

	// AllowMockPayment opts a public deployment into the mock gateway, which
	// auto-confirms every top-up after 2s without taking real money. It is
	// otherwise refused whenever SiteURL points at a non-local host, so a
	// misconfigured production server can never silently hand out free credit.
	// Leave false in production. Local dev (localhost SiteURL) needs no flag.
	AllowMockPayment bool `yaml:"allow_mock_payment,omitempty"`

	// MaxOverdraftUSD caps how far a user's USD wallet may be driven negative
	// by in-flight requests (a single huge request, or many concurrent ones,
	// can each pass the balance>0 PreCheck and then bill against a near-zero
	// balance). Charges are clamped so the wallet can never rest below
	// -MaxOverdraftUSD. nil → default 10.0; set to 0 to disable the floor
	// (unbounded negative — not recommended).
	MaxOverdraftUSD *float64 `yaml:"max_overdraft_usd"`

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

// InvoiceConfig overrides the 开票 defaults. Empty fields keep the built-in
// value, so a partial block is safe.
type InvoiceConfig struct {
	// TitleSuggestURL is the company-name suggest endpoint proxied for the
	// 抬头 picker. Defaults to 天眼查's public one.
	TitleSuggestURL string `yaml:"title_suggest_url,omitempty"`
	// The 对公转账 destination shown after an invoice request is filed.
	AccountNo   string `yaml:"account_no,omitempty"`
	AccountName string `yaml:"account_name,omitempty"`
	BankBranch  string `yaml:"bank_branch,omitempty"`
	BankCode    string `yaml:"bank_code,omitempty"`
}

// SignupFraudConfig tunes the welcome-bonus anti-abuse check (internal/saas/
// growth/fraud.go). A new signup is flagged — and its bonus withheld — when its
// browser fingerprint matches a prior user, or when its /24 (IPv6 /48) subnet
// produced at least IPSubnetThreshold distinct signups within WindowHours.
// Enabled defaults to true; a zero threshold/window falls back to 3 / 720h.
type SignupFraudConfig struct {
	Enabled           *bool `yaml:"enabled"`             // default true
	IPSubnetThreshold int   `yaml:"ip_subnet_threshold"` // default 3
	WindowHours       int   `yaml:"window_hours"`        // default 720 (30 days)
	// RequireFingerprint withholds the bonus from a signup carrying no browser
	// fingerprint at all — the shape of a scripted registration that never
	// loaded the page. Default true. Set false only if a legitimate integration
	// registers users server-side.
	RequireFingerprint *bool `yaml:"require_fingerprint"`
	// EmailDomainThreshold flags a burst of signups sharing one non-mainstream
	// email domain within the window. Default 3.
	EmailDomainThreshold int `yaml:"email_domain_threshold"`
	// DisposableDomains extends the built-in throwaway-mailbox blocklist. Exact
	// domains, or a leading "." for a suffix match (".example.tld").
	DisposableDomains []string `yaml:"disposable_domains"`
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
	if c.LocalSnapshotDays <= 0 {
		c.LocalSnapshotDays = 14
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
		// Suspended programme: no welcome credit unless an operator opts back in.
		v := 0.0
		c.SignupBonusUSD = &v
	}
	if c.ReferralsEnabled == nil {
		f := false
		c.ReferralsEnabled = &f
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
