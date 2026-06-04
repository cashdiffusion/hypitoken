// Package shop implements a minimal 发卡网 (card-shop) storefront on its own
// HTTP listener (default :8319). Buyers identify themselves by email +
// a self-chosen query password — no user accounts. Payment goes through
// the same Z-Pay aggregator the SaaS wallet uses; on a confirmed payment
// the shop pops a card secret (or returns a templated message) and emails
// it. Admin pages reuse the operator's `admin_token` for auth.
package shop

import (
	"strings"
	"time"
)

// Config is the YAML shape of the top-level `shop` block.
type Config struct {
	// Enabled gates the whole package. When false, the endpoint isn't
	// wired even if endpoints.shop is up.
	Enabled bool `yaml:"enabled"`

	// DBPath is the SQLite file holding products + card pools + orders.
	// Relative paths resolve against the dir of the loaded config file.
	DBPath string `yaml:"db_path"`

	// Site identity (used in HTML pages and outbound email).
	SiteName string `yaml:"site_name"`
	SiteURL  string `yaml:"site_url"`

	// NotifyURL is what the shop tells Z-Pay to call on payment success.
	// MUST be a public HTTPS URL the Z-Pay servers can reach. Typically
	// pointed at a reverse-proxy like Caddy → 127.0.0.1:8319/notify.
	NotifyURL string `yaml:"notify_url"`

	// ReturnURL is where the buyer's browser lands after they finish
	// paying. The shop builds per-order URLs by appending /<out_trade_no>
	// to whatever prefix is set here (e.g. https://shop.example.com/order).
	ReturnURLPrefix string `yaml:"return_url_prefix"`

	// Order pending TTL — orders not paid within this window are
	// marked expired by a background sweeper. Default 30m.
	OrderTTL time.Duration `yaml:"order_ttl"`

	// ZPay merchant credentials. Independent from saas.zpay so the shop
	// can be billed under its own merchant account if desired.
	ZPay ZPayConfig `yaml:"zpay"`

	// SMTP for transactional email (order delivery). Independent from
	// saas.smtp. Pointing the host at smtp.resend.com effectively makes
	// this a Resend setup.
	SMTP SMTPConfig `yaml:"smtp"`
}

// ZPayConfig mirrors the upstream Z-Pay merchant fields.
type ZPayConfig struct {
	BaseURL string `yaml:"base_url"` // default https://zpayz.cn
	PID     string `yaml:"pid"`
	Key     string `yaml:"key"`
}

// SMTPConfig is the same shape as saas.SMTPConfig — kept separate so the
// shop's mail isn't coupled to whether SaaS is enabled.
type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
	UseTLS   bool   `yaml:"use_tls"`
}

// ApplyDefaults fills in sensible defaults; called from config.applyDefaults.
// configDir is the dir of the loaded config.yaml so relative paths can be
// resolved without leaking that concern out to handlers.
func (c *Config) ApplyDefaults(_ string) {
	if c.OrderTTL <= 0 {
		c.OrderTTL = 30 * time.Minute
	}
	if strings.TrimSpace(c.DBPath) == "" {
		c.DBPath = "shop.db"
	}
	if strings.TrimSpace(c.SiteName) == "" {
		c.SiteName = "Card Shop"
	}
	if strings.TrimSpace(c.ZPay.BaseURL) == "" {
		c.ZPay.BaseURL = "https://zpayz.cn"
	}
	if c.SMTP.Port == 0 {
		c.SMTP.Port = 587
	}
}
