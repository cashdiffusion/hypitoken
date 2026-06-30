package market

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/billing"
)

func newTestMarket(t *testing.T) (*Market, *DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	gw, err := billing.NewZPayGateway(billing.ZPayParams{PID: "1000", Key: "testkey"})
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	cfg := Config{SiteName: "测试集市", DepositRatio: 0.10, ImageDir: filepath.Join(t.TempDir(), "img"), PickupLocation: "北京大学45甲楼下"}
	cfg.ApplyDefaults("")
	cfg.ImageDir = filepath.Join(t.TempDir(), "img2")
	m, err := New(cfg, db, gw, "secret-admin")
	if err != nil {
		t.Fatalf("new market: %v", err)
	}
	return m, db
}

func TestStorefrontRenders(t *testing.T) {
	m, db := newTestMarket(t)
	mkProduct(t, db, 120, 0.10, 3)

	eng := gin.New()
	m.RegisterRoutes(eng)

	// Homepage lists the product + its deposit.
	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("index status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "测试集市") || !strings.Contains(body, "¥12.00") {
		t.Fatalf("index missing site name or deposit: %s", body[:min(len(body), 400)])
	}

	// Product page renders the buy form.
	rec = httptest.NewRecorder()
	eng.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p/1", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "配送到楼") {
		t.Fatalf("product page bad: %d", rec.Code)
	}
}

func TestAdminGateRedirects(t *testing.T) {
	m, _ := newTestMarket(t)
	eng := gin.New()
	m.RegisterRoutes(eng)

	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/admin/login" {
		t.Fatalf("admin gate did not redirect: %d -> %q", rec.Code, rec.Header().Get("Location"))
	}

	// With the cookie, the dashboard loads.
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: adminCookie, Value: "secret-admin"}) //nolint:gosec // test-only cookie, attributes irrelevant
	rec = httptest.NewRecorder()
	eng.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "管理后台") {
		t.Fatalf("admin dashboard bad: %d", rec.Code)
	}
}

func TestSoldOutHidesBuyButton(t *testing.T) {
	m, db := newTestMarket(t)
	p := mkProduct(t, db, 50, 0.2, 1)
	if err := db.CreateOrderReserving(context.Background(), mkOrder(p, OrderPaid)); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	eng := gin.New()
	m.RegisterRoutes(eng)
	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p/1", nil))
	body := rec.Body.String()
	// Sold-out page shows the 已售罄 disabled button and omits the buy form.
	if !strings.Contains(body, "已售罄") || strings.Contains(body, `action="/p/1/buy"`) {
		t.Fatalf("sold-out product still shows buy form")
	}
}
