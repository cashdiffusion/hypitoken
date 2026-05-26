package shop

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// --- Auth pages ---

func (s *Shop) pageAdminLogin(c *gin.Context) {
	s.render(c, "admin_login.html", gin.H{
		"Error": c.Query("err"),
	})
}

func (s *Shop) handleAdminLogin(c *gin.Context) {
	pass := strings.TrimSpace(c.PostForm("password"))
	if s.adminToken == "" {
		s.renderError(c, http.StatusServiceUnavailable, "未配置管理员密码（admin_token）", nil)
		return
	}
	if pass != s.adminToken {
		c.Redirect(http.StatusFound, "/admin/login?err=%E5%AF%86%E7%A0%81%E9%94%99%E8%AF%AF")
		return
	}
	// SameSite=Lax + httponly. The cookie is the operator token itself —
	// fine because anyone with the token already has full access. TTL =
	// adminCookieTTL so sessions don't outlive a workday.
	c.SetCookie(adminCookie, s.adminToken, int(adminCookieTTL.Seconds()), "/", "", false, true)
	c.Redirect(http.StatusFound, "/admin")
}

func (s *Shop) handleAdminLogout(c *gin.Context) {
	c.SetCookie(adminCookie, "", -1, "/", "", false, true)
	c.Redirect(http.StatusFound, "/")
}

// --- Admin pages ---

func (s *Shop) pageAdminHome(c *gin.Context) {
	c.Set("is_admin", true)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	products, _ := s.db.ListProducts(ctx, false)
	orders, _ := s.db.ListOrders(ctx, "", 20)
	s.render(c, "admin_home.html", gin.H{
		"Products":     products,
		"RecentOrders": orders,
	})
}

func (s *Shop) pageAdminProducts(c *gin.Context) {
	c.Set("is_admin", true)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	products, err := s.db.ListProducts(ctx, false)
	if err != nil {
		s.renderError(c, http.StatusInternalServerError, "加载商品失败", err)
		return
	}
	s.render(c, "admin_products.html", gin.H{
		"Products": products,
		"Notice":   c.Query("ok"),
		"Error":    c.Query("err"),
	})
}

func (s *Shop) handleAdminCreateProduct(c *gin.Context) {
	p, err := parseProductForm(c)
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/products?err="+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := s.db.CreateProduct(ctx, p); err != nil {
		s.renderError(c, http.StatusInternalServerError, "创建商品失败", err)
		return
	}
	c.Redirect(http.StatusFound, "/admin/products?ok=created")
}

func (s *Shop) pageAdminProductEdit(c *gin.Context) {
	c.Set("is_admin", true)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		s.renderError(c, http.StatusBadRequest, "无效的商品 ID", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	p, err := s.db.GetProduct(ctx, id)
	if err != nil {
		s.renderError(c, http.StatusNotFound, "商品不存在", err)
		return
	}
	cards, _ := s.db.ListCardSecrets(ctx, id, true, 500)
	s.render(c, "admin_product_edit.html", gin.H{
		"Product": p,
		"Cards":   cards,
		"Notice":  c.Query("ok"),
		"Error":   c.Query("err"),
	})
}

func (s *Shop) handleAdminUpdateProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		s.renderError(c, http.StatusBadRequest, "无效的商品 ID", nil)
		return
	}
	form, err := parseProductForm(c)
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/products/"+c.Param("id")+"?err="+err.Error())
		return
	}
	form.ID = id
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := s.db.UpdateProduct(ctx, form); err != nil {
		s.renderError(c, http.StatusInternalServerError, "更新商品失败", err)
		return
	}
	c.Redirect(http.StatusFound, "/admin/products/"+c.Param("id")+"?ok=saved")
}

func (s *Shop) handleAdminDeleteProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/products")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := s.db.DeleteProduct(ctx, id); err != nil {
		s.renderError(c, http.StatusInternalServerError, "删除失败", err)
		return
	}
	c.Redirect(http.StatusFound, "/admin/products?ok=deleted")
}

func (s *Shop) handleAdminAppendCards(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		s.renderError(c, http.StatusBadRequest, "无效的商品 ID", nil)
		return
	}
	raw := c.PostForm("cards")
	lines := strings.Split(raw, "\n")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	n, err := s.db.AppendCardSecrets(ctx, id, lines)
	if err != nil {
		s.renderError(c, http.StatusInternalServerError, "导入卡密失败", err)
		return
	}
	c.Redirect(http.StatusFound, "/admin/products/"+c.Param("id")+"?ok=added-"+strconv.Itoa(n))
}

func (s *Shop) handleAdminDeleteCard(c *gin.Context) {
	cid, err := strconv.ParseInt(c.Param("card_id"), 10, 64)
	if err != nil {
		s.renderError(c, http.StatusBadRequest, "无效的卡密 ID", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := s.db.DeleteUnconsumedSecret(ctx, cid); err != nil {
		c.Redirect(http.StatusFound, "/admin/products/"+c.Param("id")+"?err="+err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/admin/products/"+c.Param("id")+"?ok=deleted")
}

func (s *Shop) pageAdminOrders(c *gin.Context) {
	c.Set("is_admin", true)
	status := c.Query("status")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	orders, err := s.db.ListOrders(ctx, status, 200)
	if err != nil {
		s.renderError(c, http.StatusInternalServerError, "加载订单失败", err)
		return
	}
	s.render(c, "admin_orders.html", gin.H{
		"Orders":      orders,
		"StatusFilter": status,
	})
}

func (s *Shop) pageAdminOrderDetail(c *gin.Context) {
	c.Set("is_admin", true)
	trade := c.Param("trade_no")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	o, err := s.db.GetOrder(ctx, trade)
	if err != nil {
		s.renderError(c, http.StatusNotFound, "订单不存在", err)
		return
	}
	s.render(c, "admin_order.html", gin.H{
		"Order":  o,
		"Notice": c.Query("ok"),
		"Error":  c.Query("err"),
	})
}

func (s *Shop) handleAdminResendEmail(c *gin.Context) {
	trade := c.Param("trade_no")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	o, err := s.db.GetOrder(ctx, trade)
	if err != nil {
		s.renderError(c, http.StatusNotFound, "订单不存在", err)
		return
	}
	if o.Status != OrderPaid && o.Status != OrderAwaitManual {
		c.Redirect(http.StatusFound, "/admin/orders/"+trade+"?err=only-paid-orders")
		return
	}
	go s.dispatchOrderEmail(o)
	c.Redirect(http.StatusFound, "/admin/orders/"+trade+"?ok=email-queued")
}

// handleAdminManualFulfil lets the operator paste a delivery message into
// an await_manual (or paid) order. Flips status to paid, optionally fires
// the buyer email.
func (s *Shop) handleAdminManualFulfil(c *gin.Context) {
	trade := c.Param("trade_no")
	body := strings.TrimSpace(c.PostForm("fulfillment"))
	if body == "" {
		c.Redirect(http.StatusFound, "/admin/orders/"+trade+"?err=empty")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := s.db.SetFulfillment(ctx, trade, body); err != nil {
		c.Redirect(http.StatusFound, "/admin/orders/"+trade+"?err="+err.Error())
		return
	}
	o, err := s.db.GetOrder(ctx, trade)
	if err == nil && c.PostForm("send_email") == "1" {
		go s.dispatchOrderEmail(o)
	}
	if err != nil {
		log.Warnf("shop: post-fulfil reload: %v", err)
	}
	c.Redirect(http.StatusFound, "/admin/orders/"+trade+"?ok=fulfilled")
}

// parseProductForm extracts a Product from POST form values. Returns a
// short ASCII error string fit for embedding in a redirect URL.
func parseProductForm(c *gin.Context) (*Product, error) {
	price, err := strconv.ParseFloat(strings.TrimSpace(c.PostForm("price_cny")), 64)
	if err != nil || price <= 0 {
		return nil, errFormBadPrice
	}
	sort, _ := strconv.Atoi(c.PostForm("sort_order"))
	delivery := strings.TrimSpace(c.PostForm("delivery_type"))
	if delivery != DeliveryAuto && delivery != DeliveryCard {
		delivery = DeliveryAuto
	}
	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		return nil, errFormBadName
	}
	return &Product{
		Name:             name,
		Description:      strings.TrimSpace(c.PostForm("description")),
		PriceCNY:         price,
		DeliveryType:     delivery,
		DeliveryTemplate: c.PostForm("delivery_template"),
		Active:           c.PostForm("active") == "1",
		SortOrder:        sort,
	}, nil
}

// Short error sentinels for the redirect URL "err" query. URL-safe.
type formErr string

func (e formErr) Error() string { return string(e) }

const (
	errFormBadPrice = formErr("bad-price")
	errFormBadName  = formErr("name-required")
)
