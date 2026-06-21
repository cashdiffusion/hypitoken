package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/admin"
	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/logging"
	"github.com/wjsoj/CPA-Claude/internal/saas"
	saasadapter "github.com/wjsoj/CPA-Claude/internal/saas/adapter"
	saasadmin "github.com/wjsoj/CPA-Claude/internal/saas/admin"
	"github.com/wjsoj/CPA-Claude/internal/saas/analytics"
	"github.com/wjsoj/CPA-Claude/internal/saas/arena"
	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/growth"
	"github.com/wjsoj/CPA-Claude/internal/saas/health"
	"github.com/wjsoj/CPA-Claude/internal/saas/mail"
	saasprofile "github.com/wjsoj/CPA-Claude/internal/saas/profile"
	"github.com/wjsoj/CPA-Claude/internal/saas/referral"
	"github.com/wjsoj/CPA-Claude/internal/saas/tokens"
	"github.com/wjsoj/CPA-Claude/internal/server"
	"github.com/wjsoj/CPA-Claude/internal/shop"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/clienttoken"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/usage"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	// Subcommand dispatch (before flag.Parse so the default server path is
	// untouched). os.Args[1] is never a flag in normal use — --config/--version
	// start with "-" — so this is unambiguous and existing invocations
	// (including the systemd ExecStart=... --config ...) keep working.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "backup":
			runBackupCmd(os.Args[2:])
			return
		case "restore":
			runRestoreCmd(os.Args[2:])
			return
		}
	}

	configPath := flag.String("config", "config.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("cpa-claude %s (commit=%s built=%s)\n", version, commit, buildDate)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	logging.Setup(cfg.LogLevel)

	log.Infof("loading credentials from %s", cfg.AuthDir)
	oauths, apikeysFromDir, err := auth.LoadAuthDir(cfg.AuthDir)
	if err != nil {
		log.Fatalf("load auth dir: %v", err)
	}
	log.Infof("loaded %d OAuth credential(s)", len(oauths))
	for _, a := range oauths {
		log.Infof("  - %s (label=%s proxy=%q max_concurrent=%d)", a.ID, a.Label, a.ProxyURL, a.MaxConcurrent)
	}
	log.Infof("loaded %d API-key credential(s) from auth_dir", len(apikeysFromDir))
	for _, a := range apikeysFromDir {
		log.Infof("  - %s (label=%s proxy=%q)", a.ID, a.Label, a.ProxyURL)
	}

	apikeys := apikeysFromDir
	for i, k := range cfg.APIKeys {
		if strings.TrimSpace(k.Key) == "" {
			continue
		}
		label := k.Label
		if label == "" {
			label = fmt.Sprintf("apikey-%d", i+1)
		}
		proxy := k.ProxyURL
		if proxy == "" {
			proxy = cfg.DefaultProxyURL
		}
		apikeys = append(apikeys, &auth.Auth{
			ID:          "apikey:" + label,
			Kind:        auth.KindAPIKey,
			Provider:    auth.NormalizeProvider(k.Provider),
			Label:       label,
			AccessToken: k.Key,
			ProxyURL:    proxy,
			BaseURL:     k.BaseURL,
			Group:       auth.NormalizeGroup(k.Group),
			ModelMap:    k.ModelMap,
		})
	}
	log.Infof("loaded %d API key(s)", len(apikeys))

	if len(oauths) == 0 && len(apikeys) == 0 {
		if strings.TrimSpace(cfg.AdminToken) == "" {
			log.Fatalf("no upstream credentials configured and admin panel is disabled — add credentials to auth_dir or set admin_token to bootstrap from the panel")
		}
		log.Warn("no upstream credentials loaded; waiting for admin panel uploads")
	}

	store, err := usage.Open(cfg.StateFile)
	if err != nil {
		log.Fatalf("open state file: %v", err)
	}

	var reqLog *requestlog.Writer
	if cfg.LogDir != "" {
		reqLog, err = requestlog.Open(cfg.LogDir, cfg.LogRetentionDays)
		if err != nil {
			log.Fatalf("open request log: %v", err)
		}
		log.Infof("request log: writing to %s (retain %d days)", cfg.LogDir, cfg.LogRetentionDays)
	} else {
		log.Info("request log: disabled (set log_dir in config to enable)")
	}

	pool := auth.NewPool(oauths, apikeys,
		time.Duration(cfg.ActiveWindowMinutes)*time.Minute,
		cfg.UseUTLS, cfg.DefaultProxyURL)
	pool.SetUsageLoadFunc(func(authID string) int64 {
		return store.Sum5h(authID).WeightedTotal()
	})

	refresherCtx, refresherCancel := context.WithCancel(context.Background())
	go pool.RunRefresher(refresherCtx, time.Minute, 10*time.Minute)
	go pool.RunDailyAnthropicAPIKeyReset(refresherCtx)

	tokensPath := filepath.Join(filepath.Dir(cfg.StateFile), "tokens.json")
	clientTokens, err := clienttoken.Open(tokensPath)
	if err != nil {
		log.Fatalf("open client token store: %v", err)
	}
	log.Infof("client tokens: %d loaded from %s", len(clientTokens.List()), tokensPath)

	s := server.New(cfg, pool, store, reqLog, clientTokens)

	// Kiro side-channel (anthropic-format → kirobridge → kiroapi). Initialized
	// only when a token_group declares upstream=kiro; nil-safe otherwise.
	if err := s.InitKiro(); err != nil {
		log.Fatalf("kiro: init: %v", err)
	}
	if k := s.KiroAccess(); k != nil {
		s.LegacyAdmin().SetKiroAccess(k)
	}

	// SaaS layer (multi-tenant). Wires SQLite-backed user/token resolution +
	// wallet billing on top of the proxy. Disabled by default — when off,
	// nothing changes from the OSS build.
	var saasDB *saasdb.DB
	var saasShutdown []func()
	if cfg.SaaS.Enabled {
		log.Infof("SaaS: opening DB at %s", cfg.SaaS.DBPath)
		if err := cfg.SaaS.EnsureJWTSecret(); err != nil {
			log.Fatalf("saas jwt secret: %v", err)
		}
		saasDB, err = saasdb.Open(cfg.SaaS.DBPath)
		if err != nil {
			log.Fatalf("open saas db: %v", err)
		}
		bootstrapAdmin(saasDB, cfg.SaaS)
		bootstrapPricingFromCatalog(cfg.SaaS, saasDB)

		// Daily VACUUM INTO snapshot, retained for 30 days. Atomic /
		// online — request handlers don't see a pause, and each snapshot
		// is a clean self-contained .db file (no WAL/SHM siblings) which
		// makes off-host shipping a single-file copy.
		go saasDB.RunDailyBackups(refresherCtx, 30)

		mailer := mail.New(mail.SMTPConfig{
			Host: cfg.SaaS.SMTP.Host, Port: cfg.SaaS.SMTP.Port,
			Username: cfg.SaaS.SMTP.Username, Password: cfg.SaaS.SMTP.Password,
			From: cfg.SaaS.SMTP.From, UseTLS: cfg.SaaS.SMTP.UseTLS,
		}, cfg.SaaS.SiteName)
		issuer := saasauth.NewIssuer(cfg.SaaS.JWTSecret, cfg.SaaS.JWTTTL)

		freeReg := true
		if cfg.SaaS.FreeRegister != nil {
			freeReg = *cfg.SaaS.FreeRegister
		}
		authH := saasauth.NewHandler(saasDB, issuer, mailer, cfg.SaaS.SiteName, freeReg)
		if cfg.SaaS.SignupBonusUSD != nil {
			authH.TrialBonusUSD = *cfg.SaaS.SignupBonusUSD
		}
		tokensH := tokens.New(saasDB)
		rate := billing.NewRate(cfg.SaaS.ExchangeRateURL, cfg.SaaS.FallbackCNYPerUSD)
		go rate.RunRefresher(refresherCtx, time.Hour)

		gateway, gwName, err := selectPaymentGateway(cfg.SaaS)
		if err != nil {
			log.Fatalf("payment gateway: %v", err)
		}
		log.Infof("payment gateway: %s", gwName)
		billingH := billing.NewHandler(saasDB, rate, gateway, cfg.SaaS.SiteName)
		billingH.SiteURL = cfg.SaaS.SiteURL
		// Stripe runs alongside the QR gateway (not instead of it) so the
		// embedded Payment Element — card / Alipay / WeChat / crypto, charged
		// 1:1 in USD — and the legacy QR rail coexist.
		if stripeGW, err := buildStripeGateway(cfg.SaaS); err != nil {
			log.Fatalf("stripe gateway: %v", err)
		} else if stripeGW != nil {
			billingH.Stripe = stripeGW
			log.Infof("stripe gateway: enabled (currency=%s, webhook=%v)", stripeGW.Currency(), stripeGW.HasWebhookSecret())
		}
		// Background sweeper: marks pending alipay orders older than the
		// TTL as expired so late notifications can't credit them.
		go billingH.RunExpirySweeper(refresherCtx)
		// Admin "Sync from Alipay" hook — calls alipay.trade.query through
		// the same applyNotification funnel as the async notify path.
		saasadmin.Reconciler = func(_ interface{ Done() <-chan struct{} }, out string) (string, error) {
			c, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			return billingH.ReconcileOrder(c, out)
		}
		adminH := saasadmin.New(saasDB)
		credH := saasadmin.NewCred(pool, cfg.AuthDir, cfg.UseUTLS)

		// Growth (marketing-channel attribution). Self-contained module: owns
		// its own tables, credits the signup bonus through the audited wallet
		// ledger (saasDB satisfies growth.Wallet), and registers the conversion
		// hook on the auth handler so ?ref= signups are attributed.
		growthSvc := growth.New(saasDB.DB, saasDB)
		// Signup anti-abuse: withhold the welcome bonus from a device/network
		// that already registered (fingerprint match or shared-subnet burst).
		fraudEnabled := true
		if cfg.SaaS.SignupFraud.Enabled != nil {
			fraudEnabled = *cfg.SaaS.SignupFraud.Enabled
		}
		growthSvc.ConfigureFraud(growth.FraudConfig{
			Enabled:         fraudEnabled,
			SubnetThreshold: cfg.SaaS.SignupFraud.IPSubnetThreshold,
			Window:          time.Duration(cfg.SaaS.SignupFraud.WindowHours) * time.Hour,
		})
		// authH.Referral is set below to the referral service, which composes
		// growthSvc (personal invite codes first, then admin marketing channels).

		// Analytics (site-wide visitor behaviour). Self-contained module: owns
		// its own two tables (web_sessions, web_events from migration v7) and
		// only needs a *sql.DB. Captures EVERY landing-page visitor's first
		// action / dwell / flow / source, surfaced in the admin Growth tab.
		analyticsSvc := analytics.New(saasDB.DB)

		// Arena (public leaderboard + real-time "Agent office"). Self-contained:
		// owns user_profiles (migration v10) via the DB layer, fans billing
		// pulses out over SSE. Injected into the adapter so Charge can publish.
		arenaSvc := arena.New(saasDB, issuer)
		profileH := saasprofile.New(saasDB)

		// Referral (viral growth: invite cards + peer gifting + campaigns).
		// Self-contained module owning migration-v11 tables; credits through the
		// audited wallet ledger and composes growthSvc for signup anti-abuse +
		// admin-channel fallback. It becomes the auth handler's referral granter
		// (personal invite codes first, growth channels second) and gift
		// auto-claimer; the adapter calls it to release deferred inviter rewards.
		referralSvc := referral.New(saasDB, mailer, growthSvc, cfg.SaaS.SiteName, cfg.SaaS.SiteURL)
		referralSvc.StartSweeper(refresherCtx)
		authH.Referral = referralSvc
		authH.GiftClaimer = referralSvc

		// Catalog used for pricing computation (same instance as the proxy's
		// internal billing — the SaaS layer only adds the multiplier on top).
		catalog := pricing.NewCatalog(cfg.Pricing)
		adapter := saasadapter.NewAdapter(saasDB, catalog, rate)
		if cfg.SaaS.MaxOverdraftUSD != nil {
			adapter.MaxOverdraftUSD = *cfg.SaaS.MaxOverdraftUSD
		}
		adapter.Arena = arenaSvc
		adapter.Referral = referralSvc
		s.SetSaaS(adapter)

		// Mount /api/v2/* on every gin engine the server hosts. Both Claude
		// and Codex listeners get the same SaaS routes — operators can hit
		// either port for management.
		var spaFS fs.FS
		if f, ferr := admin.SaaSDistFS(); ferr == nil {
			spaFS = f
		}
		legacyAdmin := s.LegacyAdmin()
		// SSO bridge: a logged-in SaaS user can hit /admin/api/*
		// with their JWT instead of the legacy operator token. GETs are
		// allowed for any authenticated user (so the Overview / charts /
		// Pricing tabs work for everyone signed in); mutations require
		// role=admin and are still gated by the legacy handler.
		admin.SSOAuth = func(c *gin.Context) (bool, bool) {
			tok := strings.TrimSpace(c.GetHeader("Authorization"))
			if !strings.HasPrefix(strings.ToLower(tok), "bearer ") {
				return false, false
			}
			claims, err := issuer.Parse(strings.TrimSpace(tok[len("bearer "):]))
			if err != nil {
				return false, false
			}
			return true, claims.Role == "admin"
		}
		for _, h := range s.GinEngines() {
			saasadapter.Mount(h, saasDB, authH, tokensH, billingH, adminH, credH, issuer, legacyAdmin, cfg.LogDir, catalog, growthSvc, analyticsSvc, arenaSvc, profileH, referralSvc)
			if spaFS != nil {
				saasadapter.MountSPA(h, spaFS)
			}
		}

		// Model health background checker.
		hc := health.New(saasDB, pool, cfg.SaaS.HealthCheckInterval, cfg.LogDir)
		go hc.Run(refresherCtx)
		saasadmin.HealthRefresher = hc.Refresh

		saasShutdown = append(saasShutdown, func() { _ = saasDB.Close() })
		log.Info("SaaS: enabled")
	}

	// Shop (发卡网) standalone storefront. Independent of SaaS — wires its
	// own SQLite + Z-Pay gateway + SMTP mailer onto its own gin engine,
	// attached to the server's endpoint list so Start()/Shutdown() see it.
	if cfg.Shop.Enabled && cfg.Endpoints.Shop.IsEnabled() {
		// Shop collects via Stripe hosted Checkout (card + Alipay). Secret +
		// webhook values support the @file convention to keep them out of the
		// committed config.
		shopGw, err := buildShopStripeGateway(cfg.Shop)
		if err != nil {
			log.Fatalf("shop stripe: %v", err)
		}
		shopDB, err := shop.Open(cfg.Shop.DBPath)
		if err != nil {
			log.Fatalf("shop db: %v", err)
		}
		shopMailer := shop.NewMailer(cfg.Shop.SMTP, cfg.Shop.SiteName)
		shopInst, err := shop.New(cfg.Shop, shopDB, shopGw, shopMailer, cfg.AdminToken)
		if err != nil {
			log.Fatalf("shop init: %v", err)
		}
		shopEng := gin.New()
		shopEng.Use(gin.Recovery())
		shopEng.Use(func(c *gin.Context) {
			start := time.Now()
			c.Next()
			log.Infof("[shop] %s %s %d %s", c.Request.Method, c.Request.URL.Path, c.Writer.Status(), time.Since(start))
		})
		shopInst.RegisterRoutes(shopEng)
		addr := fmt.Sprintf("%s:%d", cfg.Endpoints.Shop.Host, cfg.Endpoints.Shop.Port)
		s.AttachExtraEndpoint("shop", addr, shopEng)
		saasShutdown = append(saasShutdown, func() { _ = shopDB.Close() })
		log.Infof("shop: enabled at %s (site=%q stripe currency=%s webhook=%t)", addr, cfg.Shop.SiteName, shopGw.Currency(), shopGw.HasWebhookSecret())
	}

	for _, ep := range s.Endpoints() {
		primary := ""
		if ep.Primary {
			primary = " (primary — admin panel mounted here)"
		}
		log.Infof("endpoint %s [%s] → %s%s", ep.Name, ep.Provider, ep.Addr, primary)
	}

	// Graceful shutdown.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		log.Info("shutting down...")
		refresherCancel()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		store.Close()
		if reqLog != nil {
			reqLog.Close()
		}
		for _, fn := range saasShutdown {
			fn()
		}
	}()

	if err := s.Start(); err != nil {
		log.Infof("server stopped: %v", err)
	}
	<-shutdownDone
}

// bootstrapAdmin creates the configured admin user on first run if the users
// table is empty. Subsequent starts ignore admin_email/password.
func bootstrapAdmin(db *saasdb.DB, cfg saas.Config) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := db.CountUsers(ctx)
	if err != nil {
		log.Warnf("saas: count users: %v", err)
		return
	}
	if n > 0 {
		return
	}
	email := strings.TrimSpace(cfg.AdminEmail)
	pw := strings.TrimSpace(cfg.AdminPassword)
	if email == "" || pw == "" {
		log.Warn("saas: no admin user — set saas.admin_email + saas.admin_password to auto-bootstrap one on first start, or register via /api/v2/auth/register")
		return
	}
	hash, err := saasauth.HashPassword(pw)
	if err != nil {
		log.Warnf("saas: bootstrap admin hash: %v", err)
		return
	}
	def, err := db.DefaultGroup(ctx)
	if err != nil {
		log.Warnf("saas: default group: %v", err)
		return
	}
	u, err := db.CreateUser(ctx, email, hash, saasdb.RoleAdmin, def.ID, true)
	if err != nil {
		log.Warnf("saas: bootstrap admin: %v", err)
		return
	}
	log.Infof("saas: bootstrapped admin user id=%d email=%s", u.ID, u.Email)
}

// bootstrapPricingFromCatalog seeds nothing — the migration creates the
// default group. This hook exists so future expansions (per-provider rate
// imports, etc.) have a place to land. Intentionally a no-op for now.
func bootstrapPricingFromCatalog(_ saas.Config, _ *saasdb.DB) {}

// selectPaymentGateway picks a billing.Gateway from the SaaS config. The
// `payment_provider` field is authoritative; if empty we infer from
// which credential block is populated. Returns the gateway plus a short
// name for logging.
func selectPaymentGateway(s saas.Config) (billing.Gateway, string, error) {
	provider := strings.ToLower(strings.TrimSpace(s.PaymentProvider))
	if provider == "" {
		switch {
		case strings.TrimSpace(s.ZPay.PID) != "":
			provider = "zpay"
		case strings.TrimSpace(s.Alipay.AppID) != "":
			provider = "alipay"
		default:
			provider = "mock"
		}
	}
	// Hard guard: the mock gateway auto-confirms every order after 2s without
	// taking real money. Refuse it on a public/production deployment unless the
	// operator has explicitly opted in. This catches the common footgun of
	// shipping a server with no payment block configured.
	if provider == "mock" && !mockPaymentAllowed(s) {
		return nil, "", fmt.Errorf("refusing mock payment gateway on a public site (SiteURL=%q): configure a real gateway (payment_provider: zpay|alipay + its credentials), or set saas.allow_mock_payment: true to override for testing", strings.TrimSpace(s.SiteURL))
	}
	switch provider {
	case "zpay":
		key, err := loadKeyFile(s.ZPay.Key)
		if err != nil {
			return nil, "", fmt.Errorf("zpay key: %w", err)
		}
		gw, err := billing.NewZPayGateway(billing.ZPayParams{
			BaseURL:   s.ZPay.BaseURL,
			PID:       s.ZPay.PID,
			Key:       key,
			NotifyURL: s.ZPay.NotifyURL,
			ReturnURL: s.ZPay.ReturnURL,
		})
		if err != nil {
			return nil, "", err
		}
		return gw, "zpay (易支付 aggregator, pid=" + s.ZPay.PID + ")", nil
	case "alipay":
		gw, err := billing.NewAlipayGateway(billing.AlipayParams{
			AppID:           s.Alipay.AppID,
			PrivateKey:      s.Alipay.PrivateKey,
			AlipayPublicKey: s.Alipay.AlipayPublicKey,
			IsProduction:    s.Alipay.IsProduction,
			NotifyURL:       s.Alipay.NotifyURL,
		})
		if err != nil {
			return nil, "", err
		}
		return gw, "alipay (direct merchant)", nil
	case "mock":
		return &billing.MockGateway{}, "mock (auto-confirms after 2s — DO NOT use in production)", nil
	}
	return nil, "", fmt.Errorf("unknown payment_provider %q (want zpay|alipay|mock)", provider)
}

// mockPaymentAllowed reports whether the mock (auto-confirm) gateway may run.
// It is permitted only when explicitly opted in, or when SiteURL points at a
// local host (dev). An empty SiteURL counts as non-local: a real deployment
// sets SiteURL, so "unset" should fail closed rather than silently mock.
func mockPaymentAllowed(s saas.Config) bool {
	return s.AllowMockPayment || isLocalSiteURL(s.SiteURL)
}

// isLocalSiteURL reports whether the configured site origin is a loopback /
// development host (localhost, 127.0.0.0/8, ::1, 0.0.0.0, or a *.local /
// *.localhost name). Empty or unparsable URLs are treated as non-local.
func isLocalSiteURL(siteURL string) bool {
	siteURL = strings.TrimSpace(siteURL)
	if siteURL == "" {
		return false
	}
	if !strings.Contains(siteURL, "://") {
		siteURL = "http://" + siteURL
	}
	u, err := url.Parse(siteURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	switch {
	case host == "localhost", host == "0.0.0.0", host == "::1":
		return true
	case strings.HasSuffix(host, ".local"), strings.HasSuffix(host, ".localhost"):
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// buildStripeGateway constructs the optional Stripe gateway. Returns
// (nil, nil) when Stripe isn't configured (no enabled flag and no secret key),
// so the caller can treat "absent" and "error" distinctly. Resolves the @path
// indirection on the secret key + webhook secret.
func buildStripeGateway(s saas.Config) (*billing.StripeGateway, error) {
	sc := s.Stripe
	if !sc.Enabled && strings.TrimSpace(sc.SecretKey) == "" {
		return nil, nil // not configured
	}
	secret, err := loadKeyFile(sc.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("secret_key: %w", err)
	}
	whsec, err := loadKeyFile(sc.WebhookSecret)
	if err != nil {
		return nil, fmt.Errorf("webhook_secret: %w", err)
	}
	returnURL := strings.TrimSpace(sc.ReturnURL)
	if returnURL == "" && strings.TrimSpace(s.SiteURL) != "" {
		returnURL = strings.TrimRight(s.SiteURL, "/") + "/app/billing"
	}
	return billing.NewStripeGateway(billing.StripeParams{
		SecretKey:                  secret,
		PublishableKey:             sc.PublishableKey,
		WebhookSecret:              whsec,
		Currency:                   sc.Currency,
		PaymentMethodConfiguration: sc.PaymentMethodConfiguration,
		ReturnURL:                  returnURL,
	})
}

// buildShopStripeGateway constructs the shop's Stripe hosted-Checkout gateway.
// Resolves the @path indirection on the secret + webhook secret. The shop may
// reuse the SaaS account's secret_key with its own webhook endpoint secret.
func buildShopStripeGateway(c shop.Config) (*billing.StripeGateway, error) {
	sc := c.Stripe
	secret, err := loadKeyFile(sc.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("secret_key: %w", err)
	}
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("secret_key is required (shop now collects via Stripe)")
	}
	whsec, err := loadKeyFile(sc.WebhookSecret)
	if err != nil {
		return nil, fmt.Errorf("webhook_secret: %w", err)
	}
	return billing.NewStripeGateway(billing.StripeParams{
		SecretKey:     secret,
		WebhookSecret: whsec,
		Currency:      sc.Currency,
	})
}

// loadKeyFile resolves either an inline secret or an "@/path" reference.
// Mirrors billing.loadKey but exposed at the main package layer so we
// can keep the file-loaded value out of the YAML committed to git.
func loadKeyFile(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "@") {
		data, err := os.ReadFile(s[1:])
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	return s, nil
}
