package shop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
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

// handleCreateOrder processes the buy form. Creates a pending order,
// kicks off Z-Pay payment, then redirects to the per-order page.
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
	payMethod := strings.TrimSpace(c.PostForm("pay_method"))
	if !looksLikeEmail(email) {
		c.Redirect(http.StatusFound, fmt.Sprintf("/buy/%d?err=%s", id, url.QueryEscape("请输入有效的邮箱地址")))
		return
	}
	if payMethod != "alipay" {
		payMethod = "alipay"
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
	pay, err := s.gw.CreatePayment(ctx, billing.PayParams{
		OutTradeNo: out,
		Subject:    subject,
		TotalCNY:   p.PriceCNY,
		Method:     payMethod,
		ClientIP:   c.ClientIP(),
	})
	if err != nil {
		log.Errorf("shop: z-pay create failed: %v", err)
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
		PayMethod:     payMethod,
		PayURL:        pay.PayURL,
		QRCode:        pay.QRCode,
		RemoteIP:      c.ClientIP(),
	}
	if o.PayURL == "" && pay.Img != "" {
		o.PayURL = pay.Img
	}
	if err := s.db.CreateOrder(ctx, o); err != nil {
		log.Errorf("shop: create order: %v", err)
		s.renderError(c, http.StatusInternalServerError, "无法保存订单", err)
		return
	}

	// Stash the query password into a session-only cookie so the next page
	// can auto-render the fulfilment without forcing the buyer to type it
	// again immediately. Cookie scope is exactly this order id.
	c.SetCookie("hypishop_q_"+out, queryPass, 0, "/order/"+out, "", false, true)
	c.Redirect(http.StatusFound, "/order/"+out)
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

// handleNotify is the Z-Pay async payment notification. Z-Pay sends the
// trade params in the URL query (GET) or as form body (POST); both are
// supported. On valid notify with TRADE_SUCCESS, we mark the order paid,
// pop a card secret (if applicable), and email the buyer.
//
// Response body MUST be the literal "success" — anything else makes Z-Pay
// retry the callback on a schedule (8 attempts over ~24h).
func (s *Shop) handleNotify(c *gin.Context) {
	// Collect params from both query and form (the gateway uses GET, but
	// some redirects flip to POST — accept both).
	form := make(map[string][]string)
	for k, v := range c.Request.URL.Query() {
		form[k] = v
	}
	_ = c.Request.ParseForm()
	for k, v := range c.Request.PostForm {
		form[k] = v
	}

	note, err := s.gw.VerifyNotify(form)
	if err != nil {
		log.Warnf("shop: notify signature reject: %v", err)
		c.String(http.StatusBadRequest, "fail")
		return
	}
	if note.AppID != s.gw.AppID() {
		log.Warnf("shop: notify pid mismatch want=%s got=%s", s.gw.AppID(), note.AppID)
		c.String(http.StatusBadRequest, "fail")
		return
	}
	if !strings.EqualFold(note.TradeStatus, "TRADE_SUCCESS") {
		// Other statuses (WAIT_BUYER_PAY etc) aren't terminal — ack with
		// success so Z-Pay stops retrying.
		c.String(http.StatusOK, "success")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	// Defensive amount check — the gateway is authoritative but if a
	// fortuitous bug ever lets a $1 order trade for the QR of a $100 one,
	// we'd rather refuse than overdeliver. Compare to 2 decimals.
	o, err := s.db.GetOrder(ctx, note.OutTradeNo)
	if err != nil {
		log.Warnf("shop: notify for unknown order %s", note.OutTradeNo)
		c.String(http.StatusBadRequest, "fail")
		return
	}
	if want := fmt.Sprintf("%.2f", o.AmountCNY); want != strings.TrimSpace(note.TotalAmount) {
		log.Warnf("shop: notify amount mismatch order=%s want=%s got=%s", o.OutTradeNo, want, note.TotalAmount)
		c.String(http.StatusBadRequest, "fail")
		return
	}

	updated, err := s.db.MarkOrderPaidAndFulfil(ctx, note.OutTradeNo, note.TradeNo)
	if errors.Is(err, ErrOrderNotPending) {
		// Duplicate webhook — already credited. Ack so Z-Pay stops retrying.
		c.String(http.StatusOK, "success")
		return
	}
	if err != nil {
		log.Errorf("shop: mark paid failed: %v", err)
		c.String(http.StatusInternalServerError, "fail")
		return
	}

	// Fire the email; failures don't block the ack — admin can resend.
	go s.dispatchOrderEmail(updated)

	c.String(http.StatusOK, "success")
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
