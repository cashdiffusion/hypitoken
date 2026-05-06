package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/admin"
	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/clienttoken"
	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/logging"
	"github.com/wjsoj/CPA-Claude/internal/pricing"
	"github.com/wjsoj/CPA-Claude/internal/requestlog"
	"github.com/wjsoj/CPA-Claude/internal/saas"
	saasadapter "github.com/wjsoj/CPA-Claude/internal/saas/adapter"
	saasadmin "github.com/wjsoj/CPA-Claude/internal/saas/admin"
	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/health"
	"github.com/wjsoj/CPA-Claude/internal/saas/mail"
	"github.com/wjsoj/CPA-Claude/internal/saas/tokens"
	"github.com/wjsoj/CPA-Claude/internal/server"
	"github.com/wjsoj/CPA-Claude/internal/usage"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
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
		tokensH := tokens.New(saasDB)
		rate := billing.NewRate(cfg.SaaS.ExchangeRateURL, cfg.SaaS.FallbackCNYPerUSD)
		go rate.RunRefresher(refresherCtx, time.Hour)

		gateway, gwName, err := selectPaymentGateway(cfg.SaaS)
		if err != nil {
			log.Fatalf("payment gateway: %v", err)
		}
		log.Infof("payment gateway: %s", gwName)
		billingH := billing.NewHandler(saasDB, rate, gateway, cfg.SaaS.SiteName)
		// Background sweeper: marks pending alipay orders older than the
		// TTL as expired so late notifications can't credit them.
		go billingH.RunExpirySweeper(refresherCtx)
		// Admin "Sync from Alipay" hook — calls alipay.trade.query through
		// the same applyNotification funnel as the async notify path.
		saasadmin.Reconciler = func(ctx interface{ Done() <-chan struct{} }, out string) (string, error) {
			c, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			return billingH.ReconcileOrder(c, out)
		}
		adminH := saasadmin.New(saasDB)
		credH := saasadmin.NewCred(pool, cfg.AuthDir, cfg.UseUTLS)

		// Catalog used for pricing computation (same instance as the proxy's
		// internal billing — the SaaS layer only adds the multiplier on top).
		catalog := pricing.NewCatalog(cfg.Pricing)
		adapter := saasadapter.NewAdapter(saasDB, catalog, rate)
		s.SetSaaS(adapter)

		// Mount /api/v2/* on every gin engine the server hosts. Both Claude
		// and Codex listeners get the same SaaS routes — operators can hit
		// either port for management.
		var spaFS fs.FS
		if f, ferr := admin.SaaSDistFS(); ferr == nil {
			spaFS = f
		}
		legacyAdmin := s.LegacyAdmin()
		// SSO bridge: a logged-in SaaS user can hit /mgmt-console/api/*
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
			saasadapter.Mount(h, saasDB, authH, tokensH, billingH, adminH, credH, issuer, legacyAdmin, cfg.LogDir)
			if spaFS != nil {
				saasadapter.MountSPA(h, spaFS)
			}
		}

		// Model health background checker.
		hc := health.New(saasDB, pool, cfg.SaaS.HealthCheckInterval)
		go hc.Run(refresherCtx)
		saasadmin.HealthRefresher = hc.Refresh

		saasShutdown = append(saasShutdown, func() { _ = saasDB.Close() })
		log.Info("SaaS: enabled")
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
