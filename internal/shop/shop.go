package shop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"

	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	"github.com/wjsoj/CPA-Claude/internal/saas/mail"
)

// Shop bundles the dependencies and exposes RegisterRoutes(engine) so
// the server.go wiring can mount everything in one shot.
type Shop struct {
	cfg        Config
	db         *DB
	stripe     *billing.StripeGateway // hosted Checkout (card + Alipay)
	mailer     mail.Mailer
	adminToken string
	tpl        *templateSet

	// expirySweeperOnce guards the background goroutine — RegisterRoutes
	// may be called multiple times (per gin engine) but the sweeper must
	// run exactly once.
	expirySweeperOnce sync.Once
}

// New returns a configured Shop. mailer may be nil — the constructor will
// build one from cfg.SMTP. adminToken is the operator password used to
// gate the /admin/* surface (typically cfg.AdminToken from the top-level
// config). stripe is the hosted-Checkout payment gateway (card + Alipay).
func New(cfg Config, store *DB, stripe *billing.StripeGateway, mailer mail.Mailer, adminToken string) (*Shop, error) {
	if store == nil {
		return nil, errors.New("shop: db is required")
	}
	if stripe == nil {
		return nil, errors.New("shop: stripe gateway is required")
	}
	if mailer == nil {
		mailer = NewMailer(cfg.SMTP, cfg.SiteName)
	}
	tpl, err := parseTemplates()
	if err != nil {
		return nil, fmt.Errorf("shop: parse templates: %w", err)
	}
	return &Shop{
		cfg:        cfg,
		db:         store,
		stripe:     stripe,
		mailer:     mailer,
		adminToken: strings.TrimSpace(adminToken),
		tpl:        tpl,
	}, nil
}

// RegisterRoutes wires the shop endpoints onto engine. Safe to call once
// per gin engine; the background expiry sweeper is started lazily on first
// call.
func (s *Shop) RegisterRoutes(engine *gin.Engine) {
	// Public storefront pages.
	engine.GET("/", s.pageIndex)
	engine.GET("/buy/:id", s.pageBuy)
	engine.POST("/buy/:id", s.handleCreateOrder)
	engine.GET("/order", s.pageQuery)
	engine.POST("/order", s.handleQuery)
	engine.GET("/order/:trade_no", s.pageOrderDetail)

	// Stripe webhook (server-to-server). Public, signature-verified. Card
	// settles via checkout.session.completed; Alipay settles async via
	// checkout.session.async_payment_succeeded.
	engine.POST("/stripe/webhook", s.handleStripeWebhook)

	// JSON API (used by the storefront's small bits of JS).
	api := engine.Group("/api")
	{
		api.GET("/products", s.apiListProducts)
		api.GET("/order/:trade_no", s.apiOrderStatus)
	}

	// Admin pages — gated by /admin/login → cookie.
	engine.GET("/admin/login", s.pageAdminLogin)
	engine.POST("/admin/login", s.handleAdminLogin)
	engine.GET("/admin/logout", s.handleAdminLogout)

	admin := engine.Group("/admin")
	admin.Use(s.adminAuth())
	{
		admin.GET("", s.pageAdminHome)
		admin.GET("/", s.pageAdminHome)
		admin.GET("/products", s.pageAdminProducts)
		admin.POST("/products", s.handleAdminCreateProduct)
		admin.GET("/products/:id", s.pageAdminProductEdit)
		admin.POST("/products/:id", s.handleAdminUpdateProduct)
		admin.POST("/products/:id/delete", s.handleAdminDeleteProduct)
		admin.POST("/products/:id/cards", s.handleAdminAppendCards)
		admin.POST("/products/:id/cards/:card_id/delete", s.handleAdminDeleteCard)
		admin.GET("/orders", s.pageAdminOrders)
		admin.GET("/orders/:trade_no", s.pageAdminOrderDetail)
		admin.POST("/orders/:trade_no/resend", s.handleAdminResendEmail)
		admin.POST("/orders/:trade_no/fulfil", s.handleAdminManualFulfil)
	}

	// Healthz on the shop endpoint specifically so monitoring can poll it
	// without depending on the proxy ports.
	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "endpoint": "shop"})
	})

	s.expirySweeperOnce.Do(func() {
		go s.runExpirySweeper()
	})
}

// runExpirySweeper marks pending orders older than cfg.OrderTTL as
// expired. Runs every minute; absorbs DB errors via log.Warn so a
// transient sqlite hiccup doesn't crash the process.
func (s *Shop) runExpirySweeper() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		cutoff := time.Now().Add(-s.cfg.OrderTTL)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		n, err := s.db.ExpirePendingBefore(ctx, cutoff)
		cancel()
		if err != nil {
			log.Warnf("shop: expiry sweeper: %v", err)
			continue
		}
		if n > 0 {
			log.Infof("shop: expired %d pending order(s)", n)
		}
	}
}

// --- Admin auth: simple httponly cookie carrying a fixed token ---

const (
	adminCookie    = "hypishop_admin"
	adminCookieTTL = 30 * 24 * time.Hour
)

func (s *Shop) adminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.adminToken == "" {
			c.AbortWithStatus(503) // admin token not configured
			return
		}
		tok, _ := c.Cookie(adminCookie)
		if tok == s.adminToken {
			c.Next()
			return
		}
		c.Redirect(302, "/admin/login")
		c.Abort()
	}
}

// --- Helpers shared across handlers ---

// newOrderID returns a 16-hex-char id stable enough for Z-Pay's
// out_trade_no field (it must be unique per merchant).
func newOrderID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "S" + hex.EncodeToString(b[:]) // 'S' for shop, distinguishes from SaaS top-up IDs
}

// hashQueryPass returns a bcrypt hash of the buyer's query password.
// Cost=10 — same default as the SaaS auth.
func hashQueryPass(pass string) (string, error) {
	pass = strings.TrimSpace(pass)
	if len(pass) < 4 {
		return "", errors.New("查询密码至少 4 位")
	}
	if len(pass) > 64 {
		return "", errors.New("查询密码过长")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func checkQueryPass(hash, pass string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(strings.TrimSpace(pass))) == nil
}

// orderReturnURL returns the public URL the user lands on after paying.
// Z-Pay appends the trade params as query string; we keep the URL stable
// enough that the /order/:id route picks it up.
func (s *Shop) orderReturnURL(outTradeNo string) string {
	prefix := strings.TrimRight(s.cfg.ReturnURLPrefix, "/")
	if prefix == "" {
		prefix = strings.TrimRight(s.cfg.SiteURL, "/") + "/order"
	}
	return prefix + "/" + outTradeNo
}
