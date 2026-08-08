package support

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	saasauth "github.com/wjsoj/CPA-Claude/internal/saas/auth"
)

// Invoicing lives in the support package rather than billing because that is
// what it actually is here: a conversation with a human, not a payment flow.
// We do not issue 发票 automatically — the customer states an 抬头, transfers by
// 对公转账, and an operator handles the rest in the ticket thread. Reusing the
// desk means the whole exchange (attachments, back-and-forth about a wrong tax
// number, the eventual "invoice sent") lives in one place the operator already
// watches.

// KindInvoice is a ticket asking for a 增值税发票 against a 对公转账 payment.
const KindInvoice = "invoice"

// defaultTitleSuggestURL is 天眼查's public company-name suggest endpoint. It
// takes no credentials but is rate-limited per source IP (a few hundred lookups
// a day before it starts answering {errorCode:302004,"请登录"}), which is why
// results are cached for an hour and every failure degrades silently to
// "type it in yourself" rather than blocking the form.
const defaultTitleSuggestURL = "https://capi.tianyancha.com/cloud-tempest/search/suggest/v3"

// PaymentInfo is the 对公转账 destination shown after an invoice request is
// filed. Defaults are the company's real account; an operator can override them
// from config without a rebuild.
type PaymentInfo struct {
	AccountNo   string `json:"account_no"`
	AccountName string `json:"account_name"`
	BankBranch  string `json:"bank_branch"`
	BankCode    string `json:"bank_code"`
}

func defaultPaymentInfo() PaymentInfo {
	return PaymentInfo{
		AccountNo:   "15000593710093",
		AccountName: "深圳卡希涌流科技有限公司",
		BankBranch:  "平安银行深圳高新北支行",
		BankCode:    "307584021082",
	}
}

// ConfigureInvoicing overrides the invoice defaults from operator config. Empty
// fields keep their built-in value, so a partial config block is safe.
func (s *Service) ConfigureInvoicing(suggestURL string, pay PaymentInfo) {
	if strings.TrimSpace(suggestURL) != "" {
		s.titleSuggestURL = suggestURL
	}
	if pay.AccountNo != "" {
		s.payment.AccountNo = pay.AccountNo
	}
	if pay.AccountName != "" {
		s.payment.AccountName = pay.AccountName
	}
	if pay.BankBranch != "" {
		s.payment.BankBranch = pay.BankBranch
	}
	if pay.BankCode != "" {
		s.payment.BankCode = pay.BankCode
	}
}

// InvoiceRoutes mounts the invoice surface for signed-in users.
func (s *Service) InvoiceRoutes(g *gin.RouterGroup) {
	inv := g.Group("/invoice")
	inv.GET("/title-suggest", s.titleSuggest)
	inv.GET("/payment-info", s.paymentInfo)
	inv.POST("/request", s.invoiceRequest)
}

// InvoiceTitle is the 抬头 a customer wants on the 发票.
type InvoiceTitle struct {
	Name    string `json:"name"`
	TaxNo   string `json:"tax_no"`
	Address string `json:"address,omitempty"`
	Phone   string `json:"phone,omitempty"`
	Bank    string `json:"bank,omitempty"`
	BankAcc string `json:"bank_account,omitempty"`
}

// invoiceMeta is what gets stored on the ticket, so an operator sees the exact
// 抬头 to type onto the 发票 rather than having to parse it back out of prose.
type invoiceMeta struct {
	Kind      string       `json:"kind"` // always "invoice"; discriminates future meta shapes
	Title     InvoiceTitle `json:"title"`
	AmountCNY float64      `json:"amount_cny,omitempty"`
	Note      string       `json:"note,omitempty"`
}

func (s *Service) paymentInfo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"payment": s.payment})
}

// titleSuggest is the FALLBACK company lookup. The browser does this directly
// now (see web/src/lib/company-lookup.ts) because the provider blocks by
// geography and this service runs offshore, so from here the call currently
// cannot succeed at all — it answers errorCode 301000 "bannedLocation".
//
// It is kept rather than deleted because it costs nothing when unused and
// covers the cases the browser cannot: a customer who is themselves offshore
// (whose browser hits the same block), and a future deployment on a mainland
// host, where this path starts working again with no code change. It also
// holds the shared hourly cache, which matters if that day comes: the upstream
// budget is per source IP, so one server-side cache beats every customer
// burning their own.
func (s *Service) titleSuggest(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusOK, gin.H{"titles": []gin.H{}})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Second)
	defer cancel()

	out := []gin.H{}
	rows, err := s.fetchRemoteSuggest(ctx, q)
	if err != nil {
		// Never an error to the client: the form stays usable by hand, and a
		// lookup outage must not stop someone from requesting an invoice.
		log.Warnf("invoice: title-suggest failed for %q: %v", q, err)
		c.JSON(http.StatusOK, gin.H{"titles": out, "degraded": true})
		return
	}
	seen := map[string]bool{}
	for _, r := range rows {
		name := strings.TrimSpace(r.Name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, gin.H{"name": name, "tax_no": r.TaxNo})
		if len(out) >= 20 {
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{"titles": out})
}

type invoiceReq struct {
	Title     InvoiceTitle `json:"title"`
	AmountCNY float64      `json:"amount_cny"`
	Note      string       `json:"note"`
}

// invoiceRequest files an invoice ticket for the signed-in user.
func (s *Service) invoiceRequest(c *gin.Context) {
	u := saasauth.CurrentUser(c)
	var req invoiceReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	req.Title.Name = clip(req.Title.Name, 120)
	req.Title.TaxNo = strings.ToUpper(strings.TrimSpace(req.Title.TaxNo))
	if req.Title.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写发票抬头"})
		return
	}
	if !isLikelyTaxNo(req.Title.TaxNo) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "税号格式不正确（应为 15–20 位统一社会信用代码）"})
		return
	}
	if ok, retry := s.codeRL.allowSubmit(u.Email, c.ClientIP()); !ok {
		c.Header("Retry-After", fmt.Sprint(retry))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "提交过于频繁，请稍后再试", "retry_after": retry})
		return
	}

	meta := invoiceMeta{
		Kind:      KindInvoice,
		Title:     req.Title,
		AmountCNY: req.AmountCNY,
		Note:      clip(req.Note, 500),
	}
	metaJSON, _ := json.Marshal(meta)

	// The body restates the 抬头 in prose as well as meta. Duplication on
	// purpose: meta is for the operator UI, the body is what survives if this
	// thread is ever read as plain text (an export, a mail client, a DB dump).
	body := invoiceBody(meta)
	subject := fmt.Sprintf("开票申请 · %s", req.Title.Name)

	t, err := s.CreateWithMeta(c.Request.Context(), u.ID, u.Email, KindInvoice, subject, body, string(metaJSON))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Infof("support: invoice ticket %d filed by user %d (%s)", t.ID, u.ID, req.Title.Name)
	c.JSON(http.StatusOK, gin.H{"ticket": t, "payment": s.payment})
}

func invoiceBody(m invoiceMeta) string {
	var b strings.Builder
	b.WriteString("开票申请\n")
	fmt.Fprintf(&b, "抬头：%s\n", m.Title.Name)
	fmt.Fprintf(&b, "税号：%s\n", m.Title.TaxNo)
	if m.Title.Address != "" {
		fmt.Fprintf(&b, "地址：%s\n", m.Title.Address)
	}
	if m.Title.Phone != "" {
		fmt.Fprintf(&b, "电话：%s\n", m.Title.Phone)
	}
	if m.Title.Bank != "" || m.Title.BankAcc != "" {
		fmt.Fprintf(&b, "开户行：%s %s\n", m.Title.Bank, m.Title.BankAcc)
	}
	if m.AmountCNY > 0 {
		fmt.Fprintf(&b, "开票金额：￥%.2f\n", m.AmountCNY)
	}
	if m.Note != "" {
		fmt.Fprintf(&b, "备注：%s\n", m.Note)
	}
	return b.String()
}

// isLikelyTaxNo validates 统一社会信用代码: 15–20 chars, uppercase letters and
// digits. 18 is the post-2015 standard; 15 covers the legacy 税务登记号. The range
// is loose so HK / foreign-invested-entity edge cases still pass — rejecting a
// real customer's valid number is worse than letting an operator eyeball a
// malformed one.
func isLikelyTaxNo(s string) bool {
	if len(s) < 15 || len(s) > 20 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isDigit := c >= '0' && c <= '9'
		isUpper := c >= 'A' && c <= 'Z'
		if !isDigit && !isUpper {
			return false
		}
	}
	return true
}

type remoteSuggestRow struct {
	Name  string
	TaxNo string
}

// fetchRemoteSuggest queries the configured company-name suggest endpoint.
func (s *Service) fetchRemoteSuggest(ctx context.Context, q string) ([]remoteSuggestRow, error) {
	if rows, ok := suggestCacheGet(q); ok {
		return rows, nil
	}
	url := s.titleSuggestURL
	if url == "" {
		url = defaultTitleSuggestURL
	}
	bodyBytes, _ := json.Marshal(map[string]string{"keyword": q})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Bare UA on purpose — this endpoint's bot detector flags Chrome-shaped
	// header combinations, and a generic UA gets through more often.
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")

	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	// Success: {state:"ok", errorCode:0, data:[{comName, taxCode, …}]}
	// Rate-limited: {state:"error", errorCode:302004, data:{token:""}}
	var parsed struct {
		State     string          `json:"state"`
		ErrorCode int             `json:"errorCode"`
		Message   string          `json:"message"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	if parsed.ErrorCode != 0 || parsed.State != "ok" {
		return nil, fmt.Errorf("remote: %s (code %d)", parsed.Message, parsed.ErrorCode)
	}
	var rows []map[string]any
	if err := json.Unmarshal(parsed.Data, &rows); err != nil {
		// Alternative providers wrap the list instead of returning it bare.
		var alt struct {
			QueryList []map[string]any `json:"queryList"`
			Resultlst []map[string]any `json:"resultlst"`
		}
		if jerr := json.Unmarshal(parsed.Data, &alt); jerr != nil {
			return nil, err
		}
		rows = append(rows, alt.QueryList...)
		rows = append(rows, alt.Resultlst...)
	}
	out := make([]remoteSuggestRow, 0, len(rows))
	for _, r := range rows {
		name := pickStr(r, "comName", "resultStr", "entName", "name", "company")
		if name == "" {
			continue
		}
		out = append(out, remoteSuggestRow{
			Name:  stripHTML(name),
			TaxNo: pickStr(r, "taxCode", "creditCode", "regNo", "creditNo", "taxNo"),
		})
	}
	suggestCachePut(q, out)
	return out, nil
}

// suggestCache keeps positive lookups for an hour, capped at 512 keywords.
// The upstream budget is per source IP and shared by every customer, so
// repeated keystrokes on the same prefix must not each cost a call.
var (
	suggestCacheMu  sync.Mutex
	suggestCacheMap = map[string]suggestCacheEntry{}
)

type suggestCacheEntry struct {
	rows    []remoteSuggestRow
	expires time.Time
}

func suggestCacheGet(q string) ([]remoteSuggestRow, bool) {
	suggestCacheMu.Lock()
	defer suggestCacheMu.Unlock()
	e, ok := suggestCacheMap[q]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.rows, true
}

func suggestCachePut(q string, rows []remoteSuggestRow) {
	suggestCacheMu.Lock()
	defer suggestCacheMu.Unlock()
	if len(suggestCacheMap) >= 512 {
		// Crude eviction: drop one arbitrary entry per overflow. The hourly TTL
		// bounds long-term growth anyway.
		for k := range suggestCacheMap {
			delete(suggestCacheMap, k)
			break
		}
	}
	suggestCacheMap[q] = suggestCacheEntry{rows: rows, expires: time.Now().Add(time.Hour)}
}

func pickStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

// stripHTML removes the <em>…</em> highlight markers the suggest APIs sprinkle
// through matched substrings.
func stripHTML(s string) string {
	for _, tag := range []string{"<em>", "</em>", "<br>", "<br/>"} {
		s = strings.ReplaceAll(s, tag, "")
	}
	return strings.TrimSpace(s)
}
