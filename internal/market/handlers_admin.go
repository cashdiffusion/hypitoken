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

// --- Admin auth pages ---

func (m *Market) pageAdminLogin(c *gin.Context) {
	c.Set("is_admin", false)
	m.render(c, "admin_login.html", gin.H{"IsAdmin": false})
}

func (m *Market) handleAdminLogin(c *gin.Context) {
	if m.adminToken == "" {
		m.renderError(c, http.StatusServiceUnavailable, "管理后台未配置 admin_token", nil)
		return
	}
	if strings.TrimSpace(c.PostForm("token")) != m.adminToken {
		c.Set("is_admin", false)
		m.render(c, "admin_login.html", gin.H{"IsAdmin": false, "Error": "口令错误"})
		return
	}
	c.SetCookie(adminCookie, m.adminToken, int(adminCookieTTL.Seconds()), "/", "", false, true)
	c.Redirect(http.StatusFound, "/admin")
}

func (m *Market) handleAdminLogout(c *gin.Context) {
	c.SetCookie(adminCookie, "", -1, "/", "", false, true)
	c.Redirect(http.StatusFound, "/admin/login")
}

// --- Admin dashboard ---

func (m *Market) pageAdminHome(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	products, err := m.db.ListProducts(ctx, false)
	if err != nil {
		m.renderError(c, http.StatusInternalServerError, "无法加载商品", err)
		return
	}
	orders, err := m.db.ListOrders(ctx, "", 20)
	if err != nil {
		m.renderError(c, http.StatusInternalServerError, "无法加载订单", err)
		return
	}
	// Quick KPIs.
	var paidCount int
	var depositSum float64
	for _, o := range orders {
		if o.Status == OrderPaid || o.Status == OrderFulfilled {
			paidCount++
			depositSum += o.DepositAmount
		}
	}
	m.render(c, "admin_home.html", gin.H{
		"Products":   products,
		"Orders":     orders,
		"PaidCount":  paidCount,
		"DepositSum": depositSum,
	})
}

// --- Product CRUD ---

func (m *Market) pageAdminProductNew(c *gin.Context) {
	m.render(c, "admin_product_edit.html", gin.H{
		"P":     &Product{Active: true, Quantity: 1, DepositRatio: m.cfg.DepositRatio, Images: []string{}},
		"IsNew": true,
	})
}

func (m *Market) pageAdminProductEdit(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		m.renderError(c, http.StatusBadRequest, "无效的商品编号", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	p, err := m.db.GetProduct(ctx, id)
	if errors.Is(err, ErrNotFound) {
		m.renderError(c, http.StatusNotFound, "商品不存在", nil)
		return
	}
	if err != nil {
		m.renderError(c, http.StatusInternalServerError, "无法加载商品", err)
		return
	}
	m.render(c, "admin_product_edit.html", gin.H{"P": p, "IsNew": false})
}

// parseProductForm reads the shared create/edit form fields into p.
func (m *Market) parseProductForm(c *gin.Context, p *Product) error {
	p.Name = strings.TrimSpace(c.PostForm("name"))
	p.Description = strings.TrimSpace(c.PostForm("description"))
	if p.Name == "" {
		return errors.New("商品名称不能为空")
	}
	price, err := strconv.ParseFloat(strings.TrimSpace(c.PostForm("price")), 64)
	if err != nil || price <= 0 {
		return errors.New("请填写有效的价格")
	}
	p.Price = roundYuan(price)

	ratioPct, err := strconv.ParseFloat(strings.TrimSpace(c.DefaultPostForm("deposit_pct", "")), 64)
	if err != nil || ratioPct <= 0 || ratioPct > 100 {
		p.DepositRatio = m.cfg.DepositRatio
	} else {
		p.DepositRatio = ratioPct / 100
	}

	qty, err := strconv.Atoi(strings.TrimSpace(c.DefaultPostForm("quantity", "1")))
	if err != nil || qty < 1 {
		qty = 1
	}
	p.Quantity = qty

	sort, _ := strconv.Atoi(strings.TrimSpace(c.DefaultPostForm("sort_order", "0")))
	p.SortOrder = sort
	p.Active = c.PostForm("active") == "on" || c.PostForm("active") == "1"
	return nil
}

func (m *Market) handleAdminCreateProduct(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	p := &Product{Images: []string{}}
	if err := m.parseProductForm(c, p); err != nil {
		m.renderError(c, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if err := m.db.CreateProduct(ctx, p); err != nil {
		m.renderError(c, http.StatusInternalServerError, "创建失败", err)
		return
	}
	// Land on the edit page so the operator can immediately add photos.
	c.Redirect(http.StatusFound, "/admin/products/"+strconv.FormatInt(p.ID, 10))
}

func (m *Market) handleAdminUpdateProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		m.renderError(c, http.StatusBadRequest, "无效的商品编号", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	p, err := m.db.GetProduct(ctx, id)
	if errors.Is(err, ErrNotFound) {
		m.renderError(c, http.StatusNotFound, "商品不存在", nil)
		return
	}
	if err != nil {
		m.renderError(c, http.StatusInternalServerError, "无法加载商品", err)
		return
	}
	if err := m.parseProductForm(c, p); err != nil {
		m.renderError(c, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if err := m.db.UpdateProduct(ctx, p); err != nil {
		m.renderError(c, http.StatusInternalServerError, "保存失败", err)
		return
	}
	c.Redirect(http.StatusFound, "/admin/products/"+strconv.FormatInt(id, 10))
}

func (m *Market) handleAdminDeleteProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		m.renderError(c, http.StatusBadRequest, "无效的商品编号", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	// Remove on-disk images for this product first.
	if p, gerr := m.db.GetProduct(ctx, id); gerr == nil {
		for _, img := range p.Images {
			m.images.Delete(id, img)
		}
	}
	if err := m.db.DeleteProduct(ctx, id); err != nil {
		m.renderError(c, http.StatusInternalServerError, "删除失败", err)
		return
	}
	c.Redirect(http.StatusFound, "/admin")
}

// --- Product images ---

func (m *Market) handleAdminUploadImages(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		m.renderError(c, http.StatusBadRequest, "无效的商品编号", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	p, err := m.db.GetProduct(ctx, id)
	if errors.Is(err, ErrNotFound) {
		m.renderError(c, http.StatusNotFound, "商品不存在", nil)
		return
	}
	if err != nil {
		m.renderError(c, http.StatusInternalServerError, "无法加载商品", err)
		return
	}
	form, err := c.MultipartForm()
	if err != nil {
		m.renderError(c, http.StatusBadRequest, "上传解析失败", err)
		return
	}
	files := form.File["images"]
	if len(files) == 0 {
		c.Redirect(http.StatusFound, "/admin/products/"+strconv.FormatInt(id, 10))
		return
	}
	images := p.Images
	for _, fh := range files {
		if len(images) >= maxImagesPerProduct {
			break
		}
		name, serr := m.images.Save(id, fh)
		if serr != nil {
			m.renderError(c, http.StatusBadRequest, serr.Error(), nil)
			return
		}
		images = append(images, name)
	}
	if err := m.db.SetImages(ctx, id, images); err != nil {
		m.renderError(c, http.StatusInternalServerError, "保存图片失败", err)
		return
	}
	c.Redirect(http.StatusFound, "/admin/products/"+strconv.FormatInt(id, 10))
}

func (m *Market) handleAdminDeleteImage(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		m.renderError(c, http.StatusBadRequest, "无效的商品编号", nil)
		return
	}
	target := strings.TrimSpace(c.PostForm("file"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	p, err := m.db.GetProduct(ctx, id)
	if err != nil {
		m.renderError(c, http.StatusNotFound, "商品不存在", err)
		return
	}
	kept := make([]string, 0, len(p.Images))
	for _, img := range p.Images {
		if img == target {
			m.images.Delete(id, img)
			continue
		}
		kept = append(kept, img)
	}
	if err := m.db.SetImages(ctx, id, kept); err != nil {
		m.renderError(c, http.StatusInternalServerError, "删除图片失败", err)
		return
	}
	c.Redirect(http.StatusFound, "/admin/products/"+strconv.FormatInt(id, 10))
}

// --- Orders ---

func (m *Market) pageAdminOrders(c *gin.Context) {
	status := strings.TrimSpace(c.Query("status"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	orders, err := m.db.ListOrders(ctx, status, 200)
	if err != nil {
		m.renderError(c, http.StatusInternalServerError, "无法加载订单", err)
		return
	}
	m.render(c, "admin_orders.html", gin.H{"Orders": orders, "Filter": status})
}

func (m *Market) pageAdminOrderDetail(c *gin.Context) {
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
	m.render(c, "admin_order.html", gin.H{"O": o})
}

func (m *Market) handleAdminFulfil(c *gin.Context) {
	out := strings.TrimSpace(c.Param("trade_no"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := m.db.SetStatus(ctx, out, OrderFulfilled); err != nil {
		m.renderError(c, http.StatusInternalServerError, "操作失败", err)
		return
	}
	log.Infof("market: order %s marked fulfilled", out)
	c.Redirect(http.StatusFound, "/admin/orders/"+out)
}

func (m *Market) handleAdminCancel(c *gin.Context) {
	out := strings.TrimSpace(c.Param("trade_no"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := m.db.SetStatus(ctx, out, OrderCancelled); err != nil {
		m.renderError(c, http.StatusInternalServerError, "操作失败", err)
		return
	}
	log.Infof("market: order %s cancelled (unit freed)", out)
	c.Redirect(http.StatusFound, "/admin/orders/"+out)
}
