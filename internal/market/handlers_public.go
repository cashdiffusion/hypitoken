package market

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// pageIndex — storefront homepage listing active products.
func (m *Market) pageIndex(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	products, err := m.db.ListProducts(ctx, true)
	if err != nil {
		m.renderError(c, http.StatusInternalServerError, "无法加载商品列表", err)
		return
	}
	m.render(c, "index.html", gin.H{"Products": products})
}

// pageProduct — a single listing with the buy form.
func (m *Market) pageProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		m.renderError(c, http.StatusBadRequest, "无效的商品编号", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	p, err := m.db.GetProduct(ctx, id)
	if errors.Is(err, ErrNotFound) || (p != nil && !p.Active) {
		m.renderError(c, http.StatusNotFound, "商品不存在或已下架", nil)
		return
	}
	if err != nil {
		m.renderError(c, http.StatusInternalServerError, "无法加载商品", err)
		return
	}
	m.render(c, "product.html", gin.H{"P": p})
}

// handleCreateOrder — buyer submits the buy form (fulfilment + contact +
// optional dorm address), we reserve a unit and mint a Z-Pay deposit payment.
func (m *Market) handleCreateOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		m.renderError(c, http.StatusBadRequest, "无效的商品编号", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()

	p, err := m.db.GetProduct(ctx, id)
	if errors.Is(err, ErrNotFound) || (p != nil && !p.Active) {
		m.renderError(c, http.StatusNotFound, "商品不存在或已下架", nil)
		return
	}
	if err != nil {
		m.renderError(c, http.StatusInternalServerError, "无法加载商品", err)
		return
	}
	if p.SoldOut() {
		m.renderError(c, http.StatusConflict, "手慢了,该商品已售罄", nil)
		return
	}

	fulfil := strings.TrimSpace(c.PostForm("fulfil_method"))
	contact := strings.TrimSpace(c.PostForm("contact"))
	dorm := strings.TrimSpace(c.PostForm("dorm_address"))
	note := strings.TrimSpace(c.PostForm("buyer_note"))
	// Deposits are Alipay-only via Z-Pay's page-jump cashier (opens the Alipay
	// app directly on mobile — no QR).
	const payMethod = "alipay"

	if fulfil != FulfilPickup && fulfil != FulfilDelivery {
		m.renderError(c, http.StatusBadRequest, "请选择自提或配送到楼", nil)
		return
	}
	if contact == "" {
		m.renderError(c, http.StatusBadRequest, "请填写联系方式(并备注「来自本平台」)", nil)
		return
	}
	if len(contact) > 120 {
		m.renderError(c, http.StatusBadRequest, "联系方式过长", nil)
		return
	}
	if fulfil == FulfilDelivery && dorm == "" {
		m.renderError(c, http.StatusBadRequest, "配送到楼需要填写宿舍楼地址", nil)
		return
	}
	if len(dorm) > 200 || len(note) > 300 {
		m.renderError(c, http.StatusBadRequest, "填写内容过长", nil)
		return
	}

	order := &Order{
		OutTradeNo:    newOrderID(),
		ProductID:     p.ID,
		ProductName:   p.Name,
		Price:         p.Price,
		DepositAmount: p.DepositAmount(),
		DepositRatio:  p.DepositRatioEffective(), // effective % — accurate for fixed or ratio deposits
		Status:        OrderPending,
		PayMethod:     payMethod,
		FulfilMethod:  fulfil,
		Contact:       contact,
		DormAddress:   dorm,
		BuyerNote:     note,
		RemoteIP:      clientIP(c),
	}
	if order.DepositAmount <= 0 {
		order.DepositAmount = 0.01 // Z-Pay rejects zero-amount orders
	}

	// Reserve the unit atomically before charging.
	switch err := m.db.CreateOrderReserving(ctx, order); {
	case errors.Is(err, ErrSoldOut):
		m.renderError(c, http.StatusConflict, "手慢了,该商品已售罄", nil)
		return
	case errors.Is(err, ErrNotFound):
		m.renderError(c, http.StatusNotFound, "商品不存在或已下架", nil)
		return
	case err != nil:
		m.renderError(c, http.StatusInternalServerError, "下单失败,请重试", err)
		return
	}

	// Build the Z-Pay page-jump cashier URL (mobile → Alipay app directly).
	payURL := m.zpay.PageJumpURL(order.OutTradeNo, truncateRunes(m.cfg.SiteName+" 定金 · "+p.Name, 60), order.DepositAmount, payMethod)
	if err := m.db.SetPayInfo(ctx, order.OutTradeNo, payURL, "", payMethod); err != nil {
		log.Warnf("market: SetPayInfo: %v", err)
	}
	c.Redirect(http.StatusFound, "/order/"+order.OutTradeNo)
}

// pageOrderDetail — order status + payment surface. The order id is
// unguessable so it doubles as the access token.
func (m *Market) pageOrderDetail(c *gin.Context) {
	out := strings.TrimSpace(c.Param("trade_no"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	o, err := m.db.GetOrder(ctx, out)
	if errors.Is(err, ErrNotFound) {
		m.renderError(c, http.StatusNotFound, "订单不存在", nil)
		return
	}
	if err != nil {
		m.renderError(c, http.StatusInternalServerError, "无法加载订单", err)
		return
	}
	m.render(c, "order.html", gin.H{"O": o})
}

// apiOrderStatus — JSON status poll used by the order page to auto-advance
// from 待支付 to 已付定金 without a manual refresh.
func (m *Market) apiOrderStatus(c *gin.Context) {
	out := strings.TrimSpace(c.Param("trade_no"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	o, err := m.db.GetOrder(ctx, out)
	if errors.Is(err, ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": o.Status, "paid": o.Status == OrderPaid || o.Status == OrderFulfilled})
}

// handlePayReturn bounces the buyer from Z-Pay's fixed return_url to their
// per-order page using the appended out_trade_no query param.
func (m *Market) handlePayReturn(c *gin.Context) {
	out := strings.TrimSpace(c.Query("out_trade_no"))
	if out == "" {
		c.Redirect(http.StatusFound, "/")
		return
	}
	c.Redirect(http.StatusFound, "/order/"+out)
}

// handleZPayNotify — Z-Pay async callback. Verifies signature + amount, marks
// the order paid, and must return the literal "success" for Z-Pay to stop
// retrying.
func (m *Market) handleZPayNotify(c *gin.Context) {
	if err := c.Request.ParseForm(); err != nil {
		c.String(http.StatusBadRequest, "bad request")
		return
	}
	n, err := m.zpay.VerifyNotify(c.Request.Form)
	if err != nil {
		log.Warnf("market: zpay notify verify: %v", err)
		c.String(http.StatusBadRequest, "fail")
		return
	}
	if n.AppID != "" && n.AppID != m.zpay.AppID() {
		log.Warnf("market: zpay notify pid mismatch: %s", n.AppID)
		c.String(http.StatusBadRequest, "fail")
		return
	}
	if !strings.EqualFold(n.TradeStatus, "TRADE_SUCCESS") && !strings.EqualFold(n.TradeStatus, "TRADE_FINISHED") {
		// Not a success notification — ack so Z-Pay stops retrying.
		c.String(http.StatusOK, "success")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	o, err := m.db.GetOrder(ctx, n.OutTradeNo)
	if err != nil {
		log.Warnf("market: zpay notify order %s: %v", n.OutTradeNo, err)
		c.String(http.StatusBadRequest, "fail")
		return
	}
	// Amount guard — the paid amount must match the recorded deposit.
	if paid, perr := strconv.ParseFloat(strings.TrimSpace(n.TotalAmount), 64); perr == nil {
		if roundYuan(paid)+0.001 < o.DepositAmount {
			log.Warnf("market: zpay notify amount mismatch order=%s paid=%.2f want=%.2f", o.OutTradeNo, paid, o.DepositAmount)
			c.String(http.StatusBadRequest, "fail")
			return
		}
	}

	switch _, err := m.db.MarkPaid(ctx, o.OutTradeNo, n.TradeNo); {
	case err == nil:
		log.Infof("market: order %s deposit paid (¥%.2f)", o.OutTradeNo, o.DepositAmount)
	case errors.Is(err, ErrOrderNotPending):
		// Already settled — idempotent ack.
	default:
		log.Warnf("market: zpay notify mark paid: %v", err)
		c.String(http.StatusBadRequest, "fail")
		return
	}
	c.String(http.StatusOK, "success")
}

// truncateRunes caps a string to n runes (Z-Pay subject length limits).
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
