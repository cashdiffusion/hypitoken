// Package market implements a lightweight campus marketplace (摆摊/二手/预订
// 平台) on its own HTTP listener (default :8320). It is a sibling of the
// 发卡网 (internal/shop) package: it reuses the same SQLite file (shop.db)
// and the same Z-Pay aggregator gateway, but models a very different flow —
// physical goods sold for a *deposit* (a configurable fraction of the price),
// fulfilled by self-pickup or dorm delivery rather than card secrets/email.
//
// Buyers don't hold accounts. A purchase mints a pending order, the buyer pays
// the deposit through Z-Pay (Alipay/WeChat), and on confirmation the unit is
// reserved (the product shows "sold out" once its quantity is exhausted). The
// order carries the buyer's fulfilment choice + contact + (for delivery) dorm
// address so the operator can hand the goods over.
//
// Admin pages reuse the operator's `admin_token` cookie, exactly like shop.
package market

import (
	"strings"
	"time"
)

// Config is the YAML shape of the top-level `market` block.
type Config struct {
	// Enabled gates the whole package. When false, the endpoint isn't
	// wired even if endpoints.market is up.
	Enabled bool `yaml:"enabled"`

	// DBPath is the SQLite file holding products + orders. Defaults to the
	// shop's shop.db so the two storefronts share one file (separate tables,
	// separate migration tracking). Relative paths resolve against the dir
	// of the loaded config file.
	DBPath string `yaml:"db_path"`

	// ImageDir is the on-disk directory product photos are written to and
	// served from. Relative paths resolve against the config dir. Defaults
	// to "market_images".
	ImageDir string `yaml:"image_dir"`

	// Site identity (used in HTML pages).
	SiteName string `yaml:"site_name"`
	SiteURL  string `yaml:"site_url"`

	// DepositRatio is the default fraction of a product's price collected as
	// a deposit at checkout (0 < r <= 1). A product may override it. Default
	// 0.10 (10%).
	DepositRatio float64 `yaml:"deposit_ratio"`

	// PickupLocation is the human-readable self-pickup spot shown to buyers
	// who choose 自提. Default "北京大学45甲楼下".
	PickupLocation string `yaml:"pickup_location"`

	// NotifyURL is the public HTTPS URL Z-Pay calls on payment success.
	// Typically a Caddy site → 127.0.0.1:8320/notify.
	NotifyURL string `yaml:"notify_url"`

	// ReturnURLPrefix is where the buyer's browser lands after paying. Per-
	// order URLs are built by appending /<out_trade_no>. Defaults to
	// SiteURL + "/order".
	ReturnURLPrefix string `yaml:"return_url_prefix"`

	// OrderTTL — pending orders not paid within this window are marked
	// expired by a background sweeper (and their reserved unit freed).
	// Default 30m.
	OrderTTL time.Duration `yaml:"order_ttl"`

	// ZPay merchant credentials. The deposit payment always flows through
	// Z-Pay (not configurable per the product spec). Independent from
	// saas.zpay so the marketplace can bill under its own merchant account.
	ZPay ZPayConfig `yaml:"zpay"`
}

// ZPayConfig mirrors the upstream Z-Pay merchant fields.
type ZPayConfig struct {
	BaseURL string `yaml:"base_url"` // default https://zpayz.cn
	PID     string `yaml:"pid"`
	Key     string `yaml:"key"`
}

// ApplyDefaults fills in sensible defaults; called from config.applyDefaults.
func (c *Config) ApplyDefaults(_ string) {
	if c.OrderTTL <= 0 {
		c.OrderTTL = 30 * time.Minute
	}
	if strings.TrimSpace(c.DBPath) == "" {
		c.DBPath = "shop.db"
	}
	if strings.TrimSpace(c.ImageDir) == "" {
		c.ImageDir = "market_images"
	}
	if strings.TrimSpace(c.SiteName) == "" {
		c.SiteName = "校园集市"
	}
	if c.DepositRatio <= 0 || c.DepositRatio > 1 {
		c.DepositRatio = 0.10
	}
	if strings.TrimSpace(c.PickupLocation) == "" {
		c.PickupLocation = "北京大学45甲楼下"
	}
	if strings.TrimSpace(c.ZPay.BaseURL) == "" {
		c.ZPay.BaseURL = "https://zpayz.cn"
	}
}
