package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/smartwalle/alipay/v3"
	log "github.com/sirupsen/logrus"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// Gateway abstracts the Alipay client so dev/CI can swap in MockGateway
// (auto-confirms after 2s).
type Gateway interface {
	CreatePrecreate(ctx context.Context, outTradeNo, subject string, totalCNY float64) (qrCode string, err error)
	// VerifyNotify checks the RSA signature and returns the parsed Alipay
	// notify payload. Caller must additionally verify app_id, total_amount,
	// and trade_status against the on-disk order before crediting.
	VerifyNotify(req map[string][]string) (*Notification, error)
	// QueryTrade calls alipay.trade.query for a known out_trade_no — used
	// by the reconciliation endpoint to repair lost notifications.
	QueryTrade(ctx context.Context, outTradeNo string) (*Notification, error)
	// AppID returns the merchant App ID this gateway is bound to. Used in
	// notify validation.
	AppID() string
}

// Notification is the verified subset of Alipay's async notify / sync query
// fields used by the SaaS layer.
type Notification struct {
	OutTradeNo  string
	TradeNo     string
	TradeStatus string
	TotalAmount string  // raw, e.g. "68.28"
	AppID       string
	SellerID    string
}

type Handler struct {
	DB      *db.DB
	Rate    *Rate
	Gateway Gateway
	Site    string

	// OrderTTL controls how long a "pending" order is honoured before the
	// background sweeper marks it expired. After expiry, late Alipay
	// notifications are rejected — the user must create a new order.
	// Default 30 minutes; Alipay's QR codes themselves expire similarly.
	OrderTTL time.Duration

	// MaxPendingPerUser caps in-flight pending orders per user to stop a
	// hostile client from flooding the orders table with /topup spam.
	MaxPendingPerUser int

	mu sync.Mutex
	// per-user creation rate-limit timestamps (sliding 1h window)
	createdAt map[int64][]time.Time
}

func NewHandler(store *db.DB, rate *Rate, gw Gateway, site string) *Handler {
	return &Handler{
		DB: store, Rate: rate, Gateway: gw, Site: site,
		OrderTTL:          30 * time.Minute,
		MaxPendingPerUser: 5,
		createdAt:         make(map[int64][]time.Time),
	}
}

// UserRateRouteShim exposes the live exchange rate without requiring auth
// (mounted on the public /api/v2 group).
func (h *Handler) UserRateRouteShim() gin.HandlerFunc {
	return h.exchangeRate
}

// UserRoutes mounts /billing/* under an authenticated group.
func (h *Handler) UserRoutes(g *gin.RouterGroup) {
	g.GET("/balance", h.balance)
	g.GET("/transactions", h.transactions)
	g.GET("/orders", h.orders)
	g.GET("/rate", h.exchangeRate)
	g.POST("/topup", h.topup)
	g.GET("/orders/:id", h.orderStatus)
}

// PublicRoutes mounts callbacks Alipay POSTs to (no auth — verified by signature).
func (h *Handler) PublicRoutes(g *gin.RouterGroup) {
	g.POST("/billing/notify", h.notify)
}

func (h *Handler) balance(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	bal, err := h.DB.GetBalance(c.Request.Context(), u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"balance_usd": bal})
}

func (h *Handler) exchangeRate(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"cny_per_usd": h.Rate.CNYPerUSD(),
		"as_of":       h.Rate.AsOf().Unix(),
	})
}

func (h *Handler) transactions(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	txs, err := h.DB.ListWalletTx(c.Request.Context(), u.ID, 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(txs))
	for _, t := range txs {
		out = append(out, gin.H{
			"id":         t.ID,
			"kind":       t.Kind,
			"amount_usd": t.AmountUSD,
			"ref":        t.Ref,
			"note":       t.Note,
			"created_at": t.CreatedAt.Unix(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"transactions": out})
}

func (h *Handler) orders(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	os, err := h.DB.ListOrders(c.Request.Context(), u.ID, 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(os))
	for _, o := range os {
		out = append(out, orderView(o))
	}
	c.JSON(http.StatusOK, gin.H{"orders": out})
}

type topupReq struct {
	USD float64 `json:"usd"` // wallet credit requested (real USD)
}

func (h *Handler) topup(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	var req topupReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if !math.IsInf(req.USD, 0) && !math.IsNaN(req.USD) {
		// fall through
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid amount"})
		return
	}
	if req.USD < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "min top-up is $1"})
		return
	}
	if req.USD > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max top-up is $1000"})
		return
	}
	// Per-user creation rate limit + max-pending guard. Prevents flooding
	// the orders table or hammering Alipay's create-trade API.
	if ok, retry := h.allowCreate(u.ID); !ok {
		c.Header("Retry-After", strconv.Itoa(retry))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many top-up requests", "retry_after": retry})
		return
	}
	if pending, _ := h.DB.CountPendingOrders(c.Request.Context(), u.ID); pending >= h.MaxPendingPerUser {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":           "too many pending orders",
			"pending_orders":  pending,
			"max_pending":     h.MaxPendingPerUser,
			"hint":            "wait for existing orders to expire or close them via the Billing page",
		})
		return
	}
	rate := h.Rate.CNYPerUSD()
	cny := round2(req.USD * rate)
	out, err := genOutTradeNo(u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	subject := fmt.Sprintf("%s wallet top-up: $%.2f", h.Site, req.USD)
	qr, err := h.Gateway.CreatePrecreate(c.Request.Context(), out, subject, cny)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "alipay: " + err.Error()})
		return
	}
	if err := h.DB.CreateOrder(c.Request.Context(), db.AlipayOrder{
		OutTradeNo: out, UserID: u.ID, CNYAmount: cny, USDCredit: req.USD,
		Rate: rate, QRCode: qr,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if mock, ok := h.Gateway.(*MockGateway); ok {
		go mock.AutoConfirm(h, out)
	}
	c.JSON(http.StatusOK, gin.H{
		"out_trade_no": out,
		"cny_amount":   cny,
		"usd_credit":   req.USD,
		"rate":         rate,
		"qr_code":      qr,
	})
}

func (h *Handler) orderStatus(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	out := c.Param("id")
	o, err := h.DB.GetOrder(c.Request.Context(), out)
	if err != nil || o.UserID != u.ID {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, orderView(o))
}

func (h *Handler) notify(c *gin.Context) {
	if err := c.Request.ParseForm(); err != nil {
		c.String(http.StatusBadRequest, "fail")
		return
	}
	n, err := h.Gateway.VerifyNotify(c.Request.PostForm)
	if err != nil {
		log.Warnf("alipay notify: verify failed: %v", err)
		c.String(http.StatusBadRequest, "fail")
		return
	}
	if err := h.applyNotification(c.Request.Context(), n, "alipay-notify"); err != nil {
		// Distinguish recoverable (transient DB issue) from terminal
		// (validation rejection). For terminal we still ACK with "success"
		// so Alipay stops retrying — re-trying a tampered notify forever
		// helps no-one. The rejection is permanent.
		if errors.Is(err, ErrOrderTampered) || errors.Is(err, ErrOrderExpired) || errors.Is(err, ErrOrderUnknown) {
			log.Warnf("alipay notify: rejected (terminal): out=%s reason=%v", n.OutTradeNo, err)
			c.String(http.StatusOK, "success") // ACK; never re-process
			return
		}
		log.Warnf("alipay notify: transient credit failure for %s: %v", n.OutTradeNo, err)
		c.String(http.StatusInternalServerError, "fail") // Alipay will retry
		return
	}
	c.String(http.StatusOK, "success")
}

// applyNotification is the single funnel through which an order can be
// credited — both the async notify path and the manual reconciliation path
// must call this. Performs full validation:
//   1. trade_status is a settled value
//   2. app_id matches our configured AppID
//   3. seller_id matches the stored value if we recorded one (best effort)
//   4. total_amount matches the order's CNY amount (rounded to 2 decimals)
//   5. order is still pending (idempotent — repeat retries no-op)
//   6. order has not expired
// Successful path atomically marks paid + credits wallet inside one tx.
func (h *Handler) applyNotification(ctx context.Context, n *Notification, source string) error {
	o, err := h.DB.GetOrder(ctx, n.OutTradeNo)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return ErrOrderUnknown
		}
		return err
	}
	// Idempotent fast-path. Verify trade_no matches what we already stored
	// — different trade_no on a paid order is anomalous (replay across
	// orders), log loudly but don't double-credit.
	if o.Status == db.OrderPaid {
		if o.TradeNo != "" && n.TradeNo != "" && o.TradeNo != n.TradeNo {
			log.Warnf("alipay %s: trade_no mismatch on already-paid order %s (have=%s, got=%s)", source, o.OutTradeNo, o.TradeNo, n.TradeNo)
		}
		return nil
	}
	if o.Status == db.OrderExpired || o.Status == db.OrderFailed {
		return ErrOrderExpired
	}
	// Settled status only.
	if !strings.EqualFold(n.TradeStatus, "TRADE_SUCCESS") && !strings.EqualFold(n.TradeStatus, "TRADE_FINISHED") {
		return fmt.Errorf("trade_status %q not settled", n.TradeStatus)
	}
	// app_id must match this gateway's merchant ID. Prevents replay of a
	// notification captured against another merchant's account.
	if want := h.Gateway.AppID(); want != "" && n.AppID != "" && n.AppID != want {
		return fmt.Errorf("%w: app_id mismatch (got=%s want=%s)", ErrOrderTampered, n.AppID, want)
	}
	// total_amount must match. Compare on rounded fen to dodge float drift.
	got, perr := strconv.ParseFloat(strings.TrimSpace(n.TotalAmount), 64)
	if perr != nil {
		return fmt.Errorf("%w: bad total_amount %q", ErrOrderTampered, n.TotalAmount)
	}
	if !sameMoney(got, o.CNYAmount) {
		return fmt.Errorf("%w: amount mismatch (got=%.2f want=%.2f)", ErrOrderTampered, got, o.CNYAmount)
	}
	// Reject if the order has aged past TTL — the QR is stale, late
	// notifications are auto-rejected to prevent "scan now, pay later"
	// games where an attacker holds a paid receipt for a refunded order.
	if h.OrderTTL > 0 && time.Since(o.CreatedAt) > h.OrderTTL {
		_ = h.DB.MarkOrderExpired(ctx, o.OutTradeNo) // best effort
		return ErrOrderExpired
	}

	if err := h.DB.MarkOrderPaid(ctx, n.OutTradeNo, n.TradeNo); err != nil {
		// Could be "order not pending" if a concurrent request beat us —
		// safe; AddBalance below would double-credit so we bail.
		return err
	}
	if _, err := h.DB.AddBalance(ctx, o.UserID, db.TxKindTopup, o.USDCredit, o.OutTradeNo,
		fmt.Sprintf("alipay top-up ¥%.2f @ %.4f (%s)", o.CNYAmount, o.Rate, source), false); err != nil {
		return err
	}
	log.Infof("alipay [%s]: credited user=%d order=%s usd=%.2f cny=%.2f trade_no=%s",
		source, o.UserID, o.OutTradeNo, o.USDCredit, o.CNYAmount, n.TradeNo)
	return nil
}

// Sentinel errors. Terminal failures end the retry loop; non-sentinel
// errors are treated as transient and let Alipay retry.
var (
	ErrOrderUnknown  = errors.New("order not found")
	ErrOrderExpired  = errors.New("order expired or closed")
	ErrOrderTampered = errors.New("order validation failed")
)

func sameMoney(a, b float64) bool {
	const epsilon = 0.005 // half a fen
	return math.Abs(a-b) < epsilon
}

func orderView(o *db.AlipayOrder) gin.H {
	paid := int64(0)
	if !o.PaidAt.IsZero() {
		paid = o.PaidAt.Unix()
	}
	return gin.H{
		"out_trade_no": o.OutTradeNo,
		"cny_amount":   o.CNYAmount,
		"usd_credit":   o.USDCredit,
		"rate":         o.Rate,
		"status":       o.Status,
		"trade_no":     o.TradeNo,
		"qr_code":      o.QRCode,
		"created_at":   o.CreatedAt.Unix(),
		"paid_at":      paid,
	}
}

// allowCreate gates per-user order creation: max 10 / hour. Returns
// (true, 0) on allow, (false, retryAfterSec) on deny.
func (h *Handler) allowCreate(userID int64) (bool, int) {
	const maxPerHour = 10
	now := time.Now()
	cutoff := now.Add(-time.Hour)
	h.mu.Lock()
	defer h.mu.Unlock()
	stamps := h.createdAt[userID]
	kept := stamps[:0]
	for _, t := range stamps {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= maxPerHour {
		retry := int(kept[0].Add(time.Hour).Sub(now).Seconds())
		if retry < 1 {
			retry = 1
		}
		h.createdAt[userID] = kept
		return false, retry
	}
	h.createdAt[userID] = append(kept, now)
	return true, 0
}

// RunExpirySweeper periodically marks pending orders older than OrderTTL as
// expired, so late notifications and reconciliation calls can't credit them.
// Cancel ctx to stop. Cheap — it's a single UPDATE.
func (h *Handler) RunExpirySweeper(ctx context.Context) {
	if h.OrderTTL <= 0 {
		return
	}
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cutoff := time.Now().Add(-h.OrderTTL)
			n, err := h.DB.ExpirePendingOrdersBefore(ctx, cutoff)
			if err != nil {
				log.Warnf("billing: expiry sweep failed: %v", err)
				continue
			}
			if n > 0 {
				log.Infof("billing: expired %d stale pending order(s) older than %s", n, h.OrderTTL)
			}
		}
	}
}

// reconcile is an admin-triggered endpoint at /api/v2/admin/orders/:id/reconcile.
// It calls alipay.trade.query for the given out_trade_no and applies the
// result through the same applyNotification funnel the async notify uses.
// Use case: Alipay's notify never arrived (network blip, firewall) but the
// user did pay — operator can repair without a manual SQL update.
func (h *Handler) ReconcileOrder(ctx context.Context, outTradeNo string) (string, error) {
	n, err := h.Gateway.QueryTrade(ctx, outTradeNo)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(n.TradeStatus, "TRADE_SUCCESS") && !strings.EqualFold(n.TradeStatus, "TRADE_FINISHED") {
		return n.TradeStatus, nil
	}
	if err := h.applyNotification(ctx, n, "admin-reconcile"); err != nil {
		return "", err
	}
	return "credited", nil
}

func genOutTradeNo(userID int64) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("HT%d%s%s", userID, time.Now().Format("20060102150405"), hex.EncodeToString(b)[:6]), nil
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// --- Gateways ----------------------------------------------------------

// MockGateway is a development stand-in. CreatePrecreate returns a fake QR
// URL. Mock notifications are synthesized from the on-disk order itself —
// they go through the same applyNotification funnel as real Alipay notifies,
// so amount checks etc. still run.
type MockGateway struct{}

const MockAppID = "mock-app-id"

func (g *MockGateway) CreatePrecreate(_ context.Context, out, subject string, _ float64) (string, error) {
	return "https://example.com/mock-alipay-qr/" + out, nil
}
func (g *MockGateway) VerifyNotify(_ map[string][]string) (*Notification, error) {
	return nil, errors.New("mock gateway does not receive notifications")
}
func (g *MockGateway) QueryTrade(_ context.Context, _ string) (*Notification, error) {
	return nil, errors.New("mock gateway does not support query")
}
func (g *MockGateway) AppID() string { return MockAppID }

// AutoConfirm is the dev-only path: the topup handler kicks this off after
// creating a mock order. After the delay it constructs a synthetic
// Notification populated from the stored order so the same validation
// machinery runs (amount, app_id, etc.) — production code paths and dev
// code paths share a single funnel.
func (g *MockGateway) AutoConfirm(h *Handler, outTradeNo string) {
	time.Sleep(2 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	o, err := h.DB.GetOrder(ctx, outTradeNo)
	if err != nil {
		log.Warnf("mock-alipay: get order: %v", err)
		return
	}
	n := &Notification{
		OutTradeNo:  o.OutTradeNo,
		TradeNo:     "MOCK-" + o.OutTradeNo,
		TradeStatus: "TRADE_SUCCESS",
		TotalAmount: fmt.Sprintf("%.2f", o.CNYAmount),
		AppID:       MockAppID,
	}
	if err := h.applyNotification(ctx, n, "mock-alipay"); err != nil {
		log.Warnf("mock-alipay: apply: %v", err)
	}
}

// AlipayGateway wraps smartwalle/alipay for live/sandbox use.
type AlipayGateway struct {
	Client    *alipay.Client
	NotifyURL string
	appID     string
}

func (g *AlipayGateway) AppID() string { return g.appID }

// AlipayParams configures a real Alipay gateway. Use loadKey()-friendly
// strings ("@/path/to/file" or inline PEM) for the key fields.
type AlipayParams struct {
	AppID            string
	PrivateKey       string
	AlipayPublicKey  string
	IsProduction     bool
	NotifyURL        string
}

func NewAlipayGateway(p AlipayParams) (*AlipayGateway, error) {
	priv, err := loadKey(p.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	cli, err := alipay.New(p.AppID, priv, p.IsProduction)
	if err != nil {
		return nil, err
	}
	pub, err := loadKey(p.AlipayPublicKey)
	if err != nil {
		return nil, fmt.Errorf("alipay public key: %w", err)
	}
	if err := cli.LoadAliPayPublicKey(pub); err != nil {
		return nil, err
	}
	return &AlipayGateway{Client: cli, NotifyURL: p.NotifyURL, appID: p.AppID}, nil
}

func (g *AlipayGateway) CreatePrecreate(ctx context.Context, out, subject string, totalCNY float64) (string, error) {
	p := alipay.TradePreCreate{}
	p.NotifyURL = g.NotifyURL
	p.OutTradeNo = out
	p.Subject = subject
	p.TotalAmount = fmt.Sprintf("%.2f", totalCNY)
	resp, err := g.Client.TradePreCreate(ctx, p)
	if err != nil {
		return "", err
	}
	if !resp.IsSuccess() {
		return "", fmt.Errorf("alipay: %s (code=%s)", resp.Msg, resp.Code)
	}
	return resp.QRCode, nil
}

func (g *AlipayGateway) VerifyNotify(form map[string][]string) (*Notification, error) {
	values := make(map[string][]string, len(form))
	for k, v := range form {
		values[k] = v
	}
	if err := g.Client.VerifySign(context.Background(), values); err != nil {
		return nil, err
	}
	flat := map[string]string{}
	for k, v := range form {
		if len(v) > 0 {
			flat[k] = v[0]
		}
	}
	return &Notification{
		OutTradeNo:  flat["out_trade_no"],
		TradeNo:     flat["trade_no"],
		TradeStatus: flat["trade_status"],
		TotalAmount: flat["total_amount"],
		AppID:       flat["app_id"],
		SellerID:    flat["seller_id"],
	}, nil
}

// QueryTrade calls alipay.trade.query — used by the operator's
// reconciliation endpoint when an async notify never arrived.
func (g *AlipayGateway) QueryTrade(ctx context.Context, outTradeNo string) (*Notification, error) {
	q := alipay.TradeQuery{OutTradeNo: outTradeNo}
	resp, err := g.Client.TradeQuery(ctx, q)
	if err != nil {
		return nil, err
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("alipay trade.query: %s (code=%s)", resp.Msg, resp.Code)
	}
	return &Notification{
		OutTradeNo:  resp.OutTradeNo,
		TradeNo:     resp.TradeNo,
		TradeStatus: string(resp.TradeStatus),
		TotalAmount: resp.TotalAmount,
		// trade.query response is keyed differently — app_id/seller_id are
		// from our config, not the response payload. Leave empty so the
		// app_id check in applyNotification skips them (it short-circuits
		// when n.AppID == "").
	}, nil
}

func loadKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "@") {
		// load from file
		data, err := readFile(s[1:])
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	return s, nil
}
