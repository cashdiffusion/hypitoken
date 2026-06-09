package shop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	stripe "github.com/stripe/stripe-go/v82"
)

// pageIndex is the storefront homepage — lists active products.
func (s *Shop) pageIndex(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	products, err := s.db.ListProducts(ctx, true)
	if err != nil {
		s.renderError(c, http.StatusInternalServerError, "无法加载商品列表", err)
		return
	}
	s.render(c, "index.html", gin.H{
		"Products": products,
	})
}

// pageBuy shows the order form for a single product.
func (s *Shop) pageBuy(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		s.renderError(c, http.StatusBadRequest, "无效的商品 ID", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	p, err := s.db.GetProduct(ctx, id)
	if errors.Is(err, ErrNotFound) || (p != nil && !p.Active) {
		s.renderError(c, http.StatusNotFound, "商品不存在或已下架", nil)
		return
	}
	if err != nil {
		s.renderError(c, http.StatusInternalServerError, "无法加载商品", err)
		return
	}
	outOfStock := p.DeliveryType == DeliveryCard && p.StockAvailable <= 0
	s.render(c, "buy.html", gin.H{
		"Product":    p,
		"OutOfStock": outOfStock,
		"FormError":  c.Query("err"),
	})
}

// handleCreateOrder processes the buy form. Creates a Stripe hosted Checkout
// Session, persists a pending order carrying the session id, then redirects the
// buyer's browser straight to the Stripe-hosted payment page (card + Alipay).
// Stripe sends them back to /order/<out>?session_id=… on success, where the
// poll path settles the order.
func (s *Shop) handleCreateOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		s.renderError(c, http.StatusBadRequest, "无效的商品 ID", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	p, err := s.db.GetProduct(ctx, id)
	if err != nil {
		s.renderError(c, http.StatusNotFound, "商品不存在", err)
		return
	}
	if !p.Active {
		s.renderError(c, http.StatusBadRequest, "商品已下架", nil)
		return
	}
	if p.DeliveryType == DeliveryCard && p.StockAvailable <= 0 {
		s.renderError(c, http.StatusBadRequest, "库存不足，请稍后再试", nil)
		return
	}

	email := strings.TrimSpace(c.PostForm("email"))
	queryPass := strings.TrimSpace(c.PostForm("query_pass"))
	if !looksLikeEmail(email) {
		c.Redirect(http.StatusFound, fmt.Sprintf("/buy/%d?err=%s", id, url.QueryEscape("请输入有效的邮箱地址")))
		return
	}
	hash, err := hashQueryPass(queryPass)
	if err != nil {
		c.Redirect(http.StatusFound, fmt.Sprintf("/buy/%d?err=%s", id, url.QueryEscape(err.Error())))
		return
	}

	out := newOrderID()
	subject := fmt.Sprintf("%s - %s", s.cfg.SiteName, p.Name)
	if len(subject) > 60 {
		subject = subject[:60]
	}
	// Stripe replaces {CHECKOUT_SESSION_ID} in success_url so the return page
	// can poll-settle. Cancel returns the buyer to the product page.
	successURL := s.successURL(out)
	cancelURL := fmt.Sprintf("%s/buy/%d", strings.TrimRight(s.cfg.SiteURL, "/"), p.ID)
	// USD products charge in USD with Adaptive Pricing (Stripe localizes the
	// presentment currency so Alipay stays eligible); CNY charges directly.
	cur := NormalizeCurrency(p.Currency)
	adaptive := cur == CurrencyUSD
	sess, err := s.stripe.CreateHostedCheckout(ctx, out, p.PriceCNY, cur, adaptive, email, successURL, cancelURL, subject, nil)
	if err != nil {
		log.Errorf("shop: stripe checkout create failed: %v", err)
		s.renderError(c, http.StatusBadGateway, "创建支付订单失败，请稍后重试", err)
		return
	}

	o := &Order{
		OutTradeNo:    out,
		ProductID:     p.ID,
		ProductName:   p.Name,
		Email:         email,
		QueryPassHash: hash,
		AmountCNY:     p.PriceCNY,
		Currency:      cur,
		PayMethod:     "stripe",
		PayURL:        sess.URL,
		PaySessionID:  sess.SessionID,
		RemoteIP:      c.ClientIP(),
	}
	if err := s.db.CreateOrder(ctx, o); err != nil {
		log.Errorf("shop: create order: %v", err)
		s.renderError(c, http.StatusInternalServerError, "无法保存订单", err)
		return
	}

	// Stash the query password into a session-only cookie so the order page
	// (where Stripe redirects back) can auto-render the fulfilment without
	// forcing the buyer to retype it. Cookie scope is exactly this order id.
	c.SetCookie("hypishop_q_"+out, queryPass, 0, "/order/"+out, "", false, true)
	// Off to Stripe's hosted Checkout page.
	c.Redirect(http.StatusFound, sess.URL)
}

// settleable reports whether an order in this status can still be settled by a
// late Stripe confirmation. pending is the normal case; expired is rescued
// because async methods (Alipay) can confirm after the expiry sweeper has run
// (abandoned redirect + webhook retry backoff past OrderTTL). Terminal states
// (paid, await_manual) are left untouched.
func settleable(status string) bool {
	return status == OrderPending || status == OrderExpired
}

// successURL builds the Stripe success_url for an order, embedding Stripe's
// {CHECKOUT_SESSION_ID} placeholder so the landing page can poll-settle.
func (s *Shop) successURL(out string) string {
	prefix := strings.TrimRight(s.cfg.Stripe.SuccessURLPrefix, "/")
	if prefix == "" {
		prefix = strings.TrimRight(s.cfg.ReturnURLPrefix, "/")
	}
	if prefix == "" {
		prefix = strings.TrimRight(s.cfg.SiteURL, "/") + "/order"
	}
	return prefix + "/" + out + "?session_id={CHECKOUT_SESSION_ID}"
}

// pageQuery renders the "look up my order" form.
func (s *Shop) pageQuery(c *gin.Context) {
	s.render(c, "query.html", gin.H{
		"Error":   c.Query("err"),
		"TradeNo": c.Query("trade_no"),
		"Email":   c.Query("email"),
	})
}

// handleQuery validates the lookup form and redirects to the order page,
// passing the query password as a one-shot cookie.
func (s *Shop) handleQuery(c *gin.Context) {
	trade := strings.TrimSpace(c.PostForm("trade_no"))
	email := strings.TrimSpace(c.PostForm("email"))
	pass := strings.TrimSpace(c.PostForm("query_pass"))
	if trade == "" || email == "" || pass == "" {
		c.Redirect(http.StatusFound, "/order?err="+url.QueryEscape("请填写完整的订单号、邮箱和查询密码"))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	o, err := s.db.GetOrder(ctx, trade)
	if err != nil || !strings.EqualFold(o.Email, email) || !checkQueryPass(o.QueryPassHash, pass) {
		c.Redirect(http.StatusFound, "/order?err="+url.QueryEscape("订单号、邮箱或查询密码不匹配"))
		return
	}
	c.SetCookie("hypishop_q_"+trade, pass, 0, "/order/"+trade, "", false, true)
	c.Redirect(http.StatusFound, "/order/"+trade)
}

// pageOrderDetail shows one order. If the query-password cookie is missing
// or wrong, displays a prompt form instead of the order body.
func (s *Shop) pageOrderDetail(c *gin.Context) {
	trade := c.Param("trade_no")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	o, err := s.db.GetOrder(ctx, trade)
	if err != nil {
		s.renderError(c, http.StatusNotFound, "订单不存在", nil)
		return
	}
	// Buyer may have just been redirected back from Stripe — settle on the
	// spot (poll path) so the page reflects payment without waiting for the
	// webhook. No-op once already paid; rescues late-settling expired orders.
	if settleable(o.Status) {
		s.maybeSettleStripeOrder(ctx, o)
		if refreshed, rerr := s.db.GetOrder(ctx, trade); rerr == nil {
			o = refreshed
		}
	}
	pass, _ := c.Cookie("hypishop_q_" + trade)
	if pass == "" || !checkQueryPass(o.QueryPassHash, pass) {
		s.render(c, "order_locked.html", gin.H{
			"TradeNo": trade,
			"Email":   maskEmail(o.Email),
		})
		return
	}
	s.render(c, "order.html", gin.H{
		"Order":    o,
		"SiteName": s.cfg.SiteName,
	})
}

// handleStripeWebhook receives Stripe events for the shop. The signature is
// verified over the raw body. Card payments settle via
// checkout.session.completed; Alipay (and other redirect methods) settle
// asynchronously via checkout.session.async_payment_succeeded — the
// `completed` event for those can arrive while payment_status is still unpaid,
// so applyStripeSession gates fulfilment strictly on payment_status==paid and
// no-ops otherwise (we wait for the async event / poll).
//
// The shop may share a Stripe account with the SaaS wallet, so this endpoint
// receives that account's full event stream. Events for non-shop orders (no
// matching out_trade_no in shop_orders) are acked 200 as a harmless no-op.
func (s *Shop) handleStripeWebhook(c *gin.Context) {
	if !s.stripe.HasWebhookSecret() {
		c.String(http.StatusServiceUnavailable, "stripe webhook not configured")
		return
	}
	const maxBody = 1 << 20 // 1 MiB — Stripe payloads are small
	payload, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBody))
	if err != nil {
		c.String(http.StatusBadRequest, "read error")
		return
	}
	event, err := s.stripe.ConstructEvent(payload, c.GetHeader("Stripe-Signature"))
	if err != nil {
		log.Warnf("shop: stripe webhook signature verify failed: %v", err)
		c.String(http.StatusBadRequest, "bad signature")
		return
	}

	switch event.Type {
	case stripe.EventTypeCheckoutSessionCompleted,
		stripe.EventTypeCheckoutSessionAsyncPaymentSucceeded:
		var sess stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
			c.String(http.StatusBadRequest, "bad payload")
			return
		}
		out := sess.Metadata["out_trade_no"]
		if out == "" {
			c.String(http.StatusOK, "ignored (no out_trade_no)")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		o, err := s.db.GetOrder(ctx, out)
		if errors.Is(err, ErrNotFound) {
			// Not a shop order (e.g. a SaaS top-up on a shared account). Ack.
			c.String(http.StatusOK, "order not found")
			return
		}
		if err != nil {
			log.Errorf("shop: stripe webhook get order %s: %v", out, err)
			c.String(http.StatusInternalServerError, "lookup failed")
			return
		}
		if err := s.applyStripeSession(ctx, o, &sess, "webhook"); err != nil {
			// Transient (DB) failure — 500 so Stripe retries.
			log.Warnf("shop: stripe webhook apply order=%s: %v", out, err)
			c.String(http.StatusInternalServerError, "apply failed")
			return
		}
	default:
		// Other event types are acknowledged and ignored.
	}
	c.String(http.StatusOK, "ok")
}

// applyStripeSession settles a shop order from an authoritative Checkout
// Session (a signed webhook payload or a live poll retrieval). It is a no-op
// when the session hasn't been paid yet (async Alipay still processing), so the
// poll path can call it repeatedly. Fulfilment (card pop + email) only fires on
// the pending→paid transition; MarkOrderPaidAndFulfil's status guard keeps it
// idempotent against webhook/poll races.
func (s *Shop) applyStripeSession(ctx context.Context, o *Order, sess *stripe.CheckoutSession, source string) error {
	if !settleable(o.Status) {
		return nil
	}
	if sess == nil {
		return errors.New("nil checkout session")
	}
	if sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		return nil // not settled yet — keep polling (async methods settle later)
	}
	if err := s.stripe.VerifyPaidSession(sess, o.AmountCNY, o.Currency); err != nil {
		// Amount/currency tampering — refuse to fulfil. Terminal; logged.
		log.Warnf("shop: stripe verify order=%s: %v", o.OutTradeNo, err)
		return nil
	}
	tradeNo := sess.ID
	if sess.PaymentIntent != nil && sess.PaymentIntent.ID != "" {
		tradeNo = sess.PaymentIntent.ID
	}
	updated, err := s.db.MarkOrderPaidAndFulfil(ctx, o.OutTradeNo, tradeNo)
	if errors.Is(err, ErrOrderNotPending) {
		return nil // already settled by a racing webhook/poll
	}
	if err != nil {
		return err
	}
	log.Infof("shop: stripe [%s] settled order=%s amount=%s%.2f(%s) session=%s status=%s",
		source, updated.OutTradeNo, curSymbol(updated.Currency), updated.AmountCNY, updated.Currency, sess.ID, updated.Status)
	// Fire the delivery email; failures don't block — admin can resend. The
	// goroutine deliberately uses its own background context (dispatchOrderEmail)
	// since it must outlive this request/webhook.
	go s.dispatchOrderEmail(updated) //nolint:gosec // G118: delivery email must outlive the request ctx, dispatchOrderEmail uses its own background ctx
	return nil
}

// maybeSettleStripeOrder is the poll path: if an order is still pending and
// carries a Stripe session id, retrieve the live session and settle it on the
// spot. This makes settlement work even when no webhook is configured/delivered
// (the buyer landing back on the order page, or the status poll, drives it).
func (s *Shop) maybeSettleStripeOrder(ctx context.Context, o *Order) {
	if o == nil || !settleable(o.Status) || o.PaySessionID == "" {
		return
	}
	sess, err := s.stripe.RetrieveSession(ctx, o.PaySessionID)
	if err != nil {
		log.Warnf("shop: stripe poll retrieve %s: %v", o.PaySessionID, err)
		return
	}
	if err := s.applyStripeSession(ctx, o, sess, "poll"); err != nil {
		log.Warnf("shop: stripe poll apply order=%s: %v", o.OutTradeNo, err)
	}
}

// dispatchOrderEmail sends the delivery email and flips email_sent.
// Runs in a background goroutine from the notify handler so the upstream
// callback isn't blocked on SMTP latency.
func (s *Shop) dispatchOrderEmail(o *Order) {
	subject, body := OrderEmail(s.cfg.SiteName, o.ProductName, o.Status, o.Fulfillment, s.orderReturnURL(o.OutTradeNo))
	if err := s.mailer.Send(o.Email, subject, body); err != nil {
		log.Errorf("shop: send order email failed to=%s out=%s: %v", o.Email, o.OutTradeNo, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.db.MarkEmailSent(ctx, o.OutTradeNo); err != nil {
		log.Warnf("shop: mark email_sent: %v", err)
	}
}

// --- JSON API ---

func (s *Shop) apiListProducts(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	products, err := s.db.ListProducts(ctx, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"products": products})
}

// apiOrderStatus exposes a minimal poll endpoint for the order page to
// auto-refresh after payment without a full reload. Requires the same
// (trade_no, email, pass) triple as the lookup form.
func (s *Shop) apiOrderStatus(c *gin.Context) {
	trade := c.Param("trade_no")
	email := strings.TrimSpace(c.Query("email"))
	pass := strings.TrimSpace(c.Query("pass"))
	if pass == "" {
		pass, _ = c.Cookie("hypishop_q_" + trade)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	o, err := s.db.GetOrder(ctx, trade)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	// Poll-settle while the buyer's order page is auto-refreshing.
	if settleable(o.Status) {
		s.maybeSettleStripeOrder(ctx, o)
		if refreshed, rerr := s.db.GetOrder(ctx, trade); rerr == nil {
			o = refreshed
		}
	}
	if email != "" && !strings.EqualFold(email, o.Email) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if !checkQueryPass(o.QueryPassHash, pass) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":      o.Status,
		"paid_at":     o.PaidAt,
		"fulfillment": o.Fulfillment,
		"email_sent":  o.EmailSent,
	})
}

// --- Misc helpers ---

func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	if strings.Count(s, "@") != 1 {
		return false
	}
	dot := strings.LastIndexByte(s, '.')
	return dot > at+1 && dot < len(s)-1
}

func maskEmail(addr string) string {
	at := strings.IndexByte(addr, '@')
	if at <= 0 {
		return "***"
	}
	user, host := addr[:at], addr[at:]
	if len(user) <= 2 {
		return user[:1] + "***" + host
	}
	return user[:2] + "***" + host
}
