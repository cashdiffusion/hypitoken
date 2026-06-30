package market

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed web/templates/*.html
var templateFS embed.FS

// templateSet holds one parsed *template.Template per page, each combining
// base.html + the page so a page's {{define "content"}} overrides base's
// default block without colliding across pages.
type templateSet struct {
	pages map[string]*template.Template
}

func parseTemplates() (*templateSet, error) {
	baseBytes, err := fs.ReadFile(templateFS, "web/templates/base.html")
	if err != nil {
		return nil, fmt.Errorf("read base.html: %w", err)
	}
	out := &templateSet{pages: map[string]*template.Template{}}
	err = fs.WalkDir(templateFS, "web/templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".html") {
			return nil
		}
		name := strings.TrimPrefix(path, "web/templates/")
		if name == "base.html" {
			return nil
		}
		data, e := fs.ReadFile(templateFS, path)
		if e != nil {
			return e
		}
		t := template.New("base.html").Funcs(tplFuncs)
		if _, e := t.Parse(string(baseBytes)); e != nil {
			return fmt.Errorf("parse base: %w", e)
		}
		if _, e := t.New(name).Parse(string(data)); e != nil {
			return fmt.Errorf("parse %s: %w", name, e)
		}
		out.pages[name] = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

var tplFuncs = template.FuncMap{
	"yuan": func(v float64) string { return fmt.Sprintf("¥%.2f", v) },
	"pct":  func(v float64) string { return fmt.Sprintf("%g%%", v*100) },
	"sub":  func(a, b float64) float64 { return roundYuan(a - b) },
	"mul":  func(a, b float64) float64 { return a * b },
	"fmtTime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.Local().Format("2006-01-02 15:04")
	},
	"statusLabel": func(s string) string {
		switch s {
		case OrderPending:
			return "待支付定金"
		case OrderPaid:
			return "已付定金"
		case OrderExpired:
			return "已过期"
		case OrderCancelled:
			return "已取消"
		case OrderFulfilled:
			return "已完成"
		}
		return s
	},
	"statusColor": func(s string) string {
		switch s {
		case OrderPaid:
			return "bg-info-soft text-info"
		case OrderFulfilled:
			return "bg-success-soft text-success"
		case OrderPending:
			return "bg-warning-soft text-warning"
		case OrderExpired, OrderCancelled:
			return "bg-muted text-muted-fg"
		}
		return "bg-muted text-muted-fg"
	},
	"fulfilLabel": func(s string) string {
		switch s {
		case FulfilPickup:
			return "自提"
		case FulfilDelivery:
			return "配送到楼"
		}
		return s
	},
}

// render writes the named template into the response.
func (m *Market) render(c *gin.Context, name string, data gin.H) {
	if data == nil {
		data = gin.H{}
	}
	data["SiteName"] = m.cfg.SiteName
	data["SiteURL"] = m.cfg.SiteURL
	data["PickupLocation"] = m.cfg.PickupLocation
	if _, ok := data["IsAdmin"]; !ok {
		data["IsAdmin"] = c.GetBool("is_admin")
	}
	t, ok := m.tpl.pages[name]
	if !ok {
		c.String(http.StatusInternalServerError, "missing template: "+name)
		return
	}
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	if err := t.ExecuteTemplate(c.Writer, "base.html", data); err != nil {
		_, _ = c.Writer.WriteString("<pre>template error: " + err.Error() + "</pre>")
	}
}

// renderError serves a small error page. err is logged, never shown.
func (m *Market) renderError(c *gin.Context, status int, message string, err error) {
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	t, ok := m.tpl.pages["error.html"]
	if !ok {
		c.String(status, message)
		return
	}
	_ = t.ExecuteTemplate(c.Writer, "base.html", gin.H{
		"SiteName":       m.cfg.SiteName,
		"SiteURL":        m.cfg.SiteURL,
		"PickupLocation": m.cfg.PickupLocation,
		"Status":         status,
		"Message":        message,
	})
	if err != nil {
		_ = c.Error(err)
	}
}
