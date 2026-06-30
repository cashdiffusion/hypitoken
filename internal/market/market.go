package market

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
)

// Market bundles the marketplace dependencies and exposes
// RegisterRoutes(engine) so server.go wiring can mount everything at once.
type Market struct {
	cfg        Config
	db         *DB
	zpay       *billing.ZPayGateway
	images     *imageStore
	adminToken string
	tpl        *templateSet

	// expirySweeperOnce guards the background goroutine — RegisterRoutes may
	// run per gin engine but the sweeper must start exactly once.
	expirySweeperOnce sync.Once
}

// New returns a configured Market. zpay is the deposit-collection gateway
// (always Z-Pay per the product spec). adminToken gates the /admin surface
// (typically cfg.AdminToken from the top-level config).
func New(cfg Config, store *DB, zpay *billing.ZPayGateway, adminToken string) (*Market, error) {
	if store == nil {
		return nil, errors.New("market: db is required")
	}
	if zpay == nil {
		return nil, errors.New("market: zpay gateway is required")
	}
	imgs, err := newImageStore(cfg.ImageDir)
	if err != nil {
		return nil, fmt.Errorf("market: image store: %w", err)
	}
	tpl, err := parseTemplates()
	if err != nil {
		return nil, fmt.Errorf("market: parse templates: %w", err)
	}
	return &Market{
		cfg:        cfg,
		db:         store,
		zpay:       zpay,
		images:     imgs,
		adminToken: strings.TrimSpace(adminToken),
		tpl:        tpl,
	}, nil
}

// RegisterRoutes wires the marketplace endpoints onto engine. Safe to call
// once per gin engine; the expiry sweeper starts lazily on first call.
func (m *Market) RegisterRoutes(engine *gin.Engine) {
	// Public storefront.
	engine.GET("/", m.pageIndex)
	engine.GET("/p/:id", m.pageProduct)
	engine.POST("/p/:id/buy", m.handleCreateOrder)
	engine.GET("/order/:trade_no", m.pageOrderDetail)
	engine.GET("/api/order/:trade_no", m.apiOrderStatus)
	// Z-Pay return redirect lands here with ?out_trade_no=… — bounce to the
	// per-order detail page (a fixed return_url can't carry a path param).
	engine.GET("/pay/return", m.handlePayReturn)

	// Z-Pay async notify (GET, signature-verified). Body must be "success".
	engine.GET("/notify", m.handleZPayNotify)
	engine.POST("/notify", m.handleZPayNotify)

	// Product images — served from disk.
	engine.GET("/img/:product_id/:file", m.serveImage)

	// Admin auth gate.
	engine.GET("/admin/login", m.pageAdminLogin)
	engine.POST("/admin/login", m.handleAdminLogin)
	engine.GET("/admin/logout", m.handleAdminLogout)

	admin := engine.Group("/admin")
	admin.Use(m.adminAuth())
	{
		admin.GET("", m.pageAdminHome)
		admin.GET("/", m.pageAdminHome)
		admin.GET("/products/new", m.pageAdminProductNew)
		admin.POST("/products", m.handleAdminCreateProduct)
		admin.GET("/products/:id", m.pageAdminProductEdit)
		admin.POST("/products/:id", m.handleAdminUpdateProduct)
		admin.POST("/products/:id/delete", m.handleAdminDeleteProduct)
		admin.POST("/products/:id/images", m.handleAdminUploadImages)
		admin.POST("/products/:id/images/delete", m.handleAdminDeleteImage)
		admin.GET("/orders", m.pageAdminOrders)
		admin.GET("/orders/:trade_no", m.pageAdminOrderDetail)
		admin.POST("/orders/:trade_no/fulfil", m.handleAdminFulfil)
		admin.POST("/orders/:trade_no/cancel", m.handleAdminCancel)
	}

	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "endpoint": "market"})
	})

	m.expirySweeperOnce.Do(func() {
		go m.runExpirySweeper()
	})
}

// runExpirySweeper marks pending orders older than cfg.OrderTTL as expired,
// freeing their reserved units. Runs every minute.
func (m *Market) runExpirySweeper() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		cutoff := time.Now().Add(-m.cfg.OrderTTL)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		n, err := m.db.ExpirePendingBefore(ctx, cutoff)
		cancel()
		if err != nil {
			log.Warnf("market: expiry sweeper: %v", err)
			continue
		}
		if n > 0 {
			log.Infof("market: expired %d pending order(s)", n)
		}
	}
}

// --- Admin auth: httponly cookie carrying the operator token ---

const (
	adminCookie    = "hypimarket_admin"
	adminCookieTTL = 30 * 24 * time.Hour
)

func (m *Market) adminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if m.adminToken == "" {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		tok, _ := c.Cookie(adminCookie)
		if tok == m.adminToken {
			c.Set("is_admin", true)
			c.Next()
			return
		}
		c.Redirect(http.StatusFound, "/admin/login")
		c.Abort()
	}
}

// newOrderID returns an unguessable order id (16 hex chars) usable as Z-Pay's
// out_trade_no. 'M' prefix distinguishes marketplace orders from shop ('S').
func newOrderID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "M" + hex.EncodeToString(b[:])
}

// clientIP returns the buyer's source IP for Z-Pay's clientip field + audit.
func clientIP(c *gin.Context) string {
	return c.ClientIP()
}
