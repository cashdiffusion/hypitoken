package shop

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

// templates holds one parsed *template.Template per page, each containing
// base.html + the page so that {{define "content"}} in the page correctly
// overrides base.html's default block. Using one shared set instead would
// collide every page's "content" definition (last-parsed wins).
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

// curSymbol maps a currency code to its display symbol (¥ for CNY, $ for USD).
func curSymbol(currency string) string {
	if NormalizeCurrency(currency) == CurrencyUSD {
		return "$"
	}
	return "¥"
}

var tplFuncs = template.FuncMap{
	"add":   func(a, b int) int { return a + b },
	"upper": strings.ToUpper,
	// dict returns an empty string-keyed int map — a safe default so KPI
	// tiles can `index` it even when OrderStats failed to load (nil .Stats).
	"dict": func() map[string]int { return map[string]int{} },
	"seq": func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	},
	"fmtCNY": func(v float64) string {
		return fmt.Sprintf("¥%.2f", v)
	},
	// money renders an amount in its currency, e.g. "¥30.00" or "$5.00".
	"money": func(v float64, currency string) string {
		return curSymbol(currency) + fmt.Sprintf("%.2f", v)
	},
	// curSymbol maps a currency code to its display symbol.
	"curSymbol": curSymbol,
	"fmtTime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.Local().Format("2006-01-02 15:04")
	},
	"statusLabel": func(s string) string {
		switch s {
		case OrderPending:
			return "待支付"
		case OrderPaid:
			return "已完成"
		case OrderExpired:
			return "已过期"
		case OrderAwaitManual:
			return "人工处理中"
		}
		return s
	},
	"statusColor": func(s string) string {
		switch s {
		case OrderPaid:
			return "bg-emerald-100 text-emerald-700"
		case OrderPending:
			return "bg-amber-100 text-amber-700"
		case OrderExpired:
			return "bg-slate-200 text-slate-600"
		case OrderAwaitManual:
			return "bg-rose-100 text-rose-700"
		}
		return "bg-slate-100 text-slate-600"
	},
	"deliveryLabel": func(s string) string {
		switch s {
		case DeliveryAuto:
			return "自动消息"
		case DeliveryCard:
			return "卡密池"
		}
		return s
	},
}

// render writes the named template into the response. The base layout
// lives in base.html and each page provides "title" and "content" blocks
// that override the base defaults.
func (s *Shop) render(c *gin.Context, name string, data gin.H) {
	if data == nil {
		data = gin.H{}
	}
	data["SiteName"] = s.cfg.SiteName
	data["SiteURL"] = s.cfg.SiteURL
	data["IsAdmin"] = c.GetBool("is_admin")
	t, ok := s.tpl.pages[name]
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

// renderError serves a small error page. err may be nil — only the
// `message` and HTTP status are required for user-facing failures; err is
// logged but never shown to the buyer.
func (s *Shop) renderError(c *gin.Context, status int, message string, err error) {
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	t, ok := s.tpl.pages["error.html"]
	if !ok {
		c.String(status, message)
		return
	}
	_ = t.ExecuteTemplate(c.Writer, "base.html", gin.H{
		"SiteName": s.cfg.SiteName,
		"SiteURL":  s.cfg.SiteURL,
		"Status":   status,
		"Message":  message,
	})
	if err != nil {
		c.Error(err) //nolint:errcheck
	}
}
