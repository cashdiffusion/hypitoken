package billing

// Z-Pay (易支付 / yi-pay) gateway. Z-Pay is a Chinese payment aggregator
// that fronts Alipay and WeChat Pay rails for individual operators who
// can't get direct merchant onboarding. The protocol is the de-facto
// 易支付 standard: form-encoded params signed with MD5 over a key-sorted
// `a=b&c=d` join + the merchant key.
//
// Reference: https://z-pay.cn/doc.html (captured 2026-05-06).

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ZPayGateway implements Gateway against the 易支付-format API exposed at
// https://zpayz.cn/. Notify scheme is GET (return body must equal the
// literal string "success") and signature is MD5 lowercase.
type ZPayGateway struct {
	BaseURL   string // default https://zpayz.cn
	PID       string // 商户ID
	Key       string // 商户密钥 — never logged
	NotifyURL string // public webhook (e.g. https://api.example.com/api/v2/billing/notify)
	ReturnURL string // browser redirect after successful payment (optional)
	HTTP      *http.Client
}

// ZPayParams configures a ZPayGateway. NotifyURL must be reachable from
// the public internet; ReturnURL is optional.
type ZPayParams struct {
	BaseURL   string
	PID       string
	Key       string
	NotifyURL string
	ReturnURL string
}

func NewZPayGateway(p ZPayParams) (*ZPayGateway, error) {
	if strings.TrimSpace(p.PID) == "" {
		return nil, errors.New("zpay: pid is required")
	}
	if strings.TrimSpace(p.Key) == "" {
		return nil, errors.New("zpay: key is required")
	}
	base := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if base == "" {
		base = "https://zpayz.cn"
	}
	return &ZPayGateway{
		BaseURL:   base,
		PID:       strings.TrimSpace(p.PID),
		Key:       strings.TrimSpace(p.Key),
		NotifyURL: strings.TrimSpace(p.NotifyURL),
		ReturnURL: strings.TrimSpace(p.ReturnURL),
		HTTP:      &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// AppID returns the PID. The applyNotification path validates AppID
// equality between the on-disk gateway and the parsed notification —
// for Z-Pay that's a `pid` match.
func (g *ZPayGateway) AppID() string { return g.PID }

// CreatePayment hits /mapi.php to mint a new order. Returns whichever of
// {payurl, qrcode, img} the upstream populates.
func (g *ZPayGateway) CreatePayment(ctx context.Context, p PayParams) (*PayResult, error) {
	method := strings.ToLower(strings.TrimSpace(p.Method))
	if method == "" {
		method = "alipay"
	}
	if method != "alipay" && method != "wxpay" {
		return nil, fmt.Errorf("zpay: unsupported method %q", method)
	}
	clientIP := strings.TrimSpace(p.ClientIP)
	if clientIP == "" {
		// mapi.php rejects an empty clientip. Fall back to a plausible
		// public address — the field is informational, not authenticated.
		clientIP = "127.0.0.1"
	}
	params := map[string]string{
		"pid":          g.PID,
		"type":         method,
		"out_trade_no": p.OutTradeNo,
		"notify_url":   g.NotifyURL,
		"name":         p.Subject,
		"money":        fmt.Sprintf("%.2f", p.TotalCNY),
		"clientip":     clientIP,
	}
	if g.ReturnURL != "" {
		params["return_url"] = g.ReturnURL
	}
	params["sign"] = SignZPay(params, g.Key)
	params["sign_type"] = "MD5"

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}

	ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx2, http.MethodPost, g.BaseURL+"/mapi.php", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var r mapiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("zpay: bad response: %w (body=%q)", err, truncate(string(body), 200))
	}
	// Code is `1` on success per spec. Some installs send int, some
	// string, hence the json.Number-friendly decode below.
	if !r.IsSuccess() {
		return nil, fmt.Errorf("zpay: %s (code=%v)", r.Msg, r.Code)
	}
	out := &PayResult{
		QRCode: r.QRCode,
		PayURL: r.PayURL,
		Img:    r.Img,
	}
	if out.PayURL == "" && r.PayURL2 != "" {
		out.PayURL = r.PayURL2
	}
	if out.QRCode == "" && out.PayURL == "" && out.Img == "" {
		return nil, errors.New("zpay: empty payment surface in response")
	}
	return out, nil
}

// VerifyNotify checks an incoming notify (GET) — verifies signature and
// extracts the canonical Notification fields. Caller still validates the
// amount, trade_status and pid against the on-disk order.
func (g *ZPayGateway) VerifyNotify(form map[string][]string) (*Notification, error) {
	flat := flatten(form)
	signed := SignZPay(flat, g.Key)
	got := strings.ToLower(strings.TrimSpace(flat["sign"]))
	if got == "" {
		return nil, errors.New("zpay: missing sign")
	}
	if got != signed {
		return nil, errors.New("zpay: signature mismatch")
	}
	tradeStatus := flat["trade_status"]
	// Z-Pay's only success status is TRADE_SUCCESS. applyNotification
	// will re-check this — we just normalize unset.
	return &Notification{
		OutTradeNo:  flat["out_trade_no"],
		TradeNo:     flat["trade_no"],
		TradeStatus: tradeStatus,
		TotalAmount: flat["money"],
		AppID:       flat["pid"],
	}, nil
}

// QueryTrade looks up an order via /api.php?act=order. Used by the
// admin reconciliation endpoint when a notify never arrived.
func (g *ZPayGateway) QueryTrade(ctx context.Context, outTradeNo string) (*Notification, error) {
	q := url.Values{}
	q.Set("act", "order")
	q.Set("pid", g.PID)
	q.Set("key", g.Key)
	q.Set("out_trade_no", outTradeNo)

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// NOTE: do not log this URL — it carries the merchant key.
	req, err := http.NewRequestWithContext(ctx2, http.MethodGet, g.BaseURL+"/api.php?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var r queryResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("zpay query: bad response: %w", err)
	}
	if !r.IsSuccess() {
		return nil, fmt.Errorf("zpay query: %s (code=%v)", r.Msg, r.Code)
	}
	// Map status code (1=paid, 0=unpaid) → Alipay-shaped TRADE_SUCCESS.
	status := ""
	switch r.StatusInt() {
	case 1:
		status = "TRADE_SUCCESS"
	case 0:
		status = "WAIT_BUYER_PAY"
	default:
		status = fmt.Sprintf("UNKNOWN(%v)", r.Status)
	}
	return &Notification{
		OutTradeNo:  r.OutTradeNo,
		TradeNo:     r.TradeNo,
		TradeStatus: status,
		TotalAmount: r.Money,
		AppID:       fmt.Sprintf("%v", r.PID),
	}, nil
}

// SignZPay computes the MD5 signature defined by the 易支付 standard:
//  1. drop sign, sign_type, and any empty values
//  2. sort remaining keys ASCII ascending
//  3. join as `k=v&k=v` (raw — no URL-encoding)
//  4. md5( joined + key ), lowercase hex
func SignZPay(params map[string]string, merchantKey string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(params[k])
	}
	sb.WriteString(merchantKey)
	sum := md5.Sum([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// flatten reduces map[string][]string to map[string]string, taking the
// first value. Same shape applyNotification consumes.
func flatten(form map[string][]string) map[string]string {
	out := make(map[string]string, len(form))
	for k, v := range form {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// mapiResponse is the loose-typed shape of /mapi.php responses. Z-Pay
// flips between integer and string for `code` across deployments, so we
// decode through json.RawMessage and inspect the bytes.
type mapiResponse struct {
	Code    json.RawMessage `json:"code"`
	Msg     string          `json:"msg"`
	OID     string          `json:"O_id"`
	TradeNo string          `json:"trade_no"`
	PayURL  string          `json:"payurl"`
	PayURL2 string          `json:"payurl2"`
	QRCode  string          `json:"qrcode"`
	Img     string          `json:"img"`
}

func (r mapiResponse) IsSuccess() bool { return rawIsOne(r.Code) }

type queryResponse struct {
	Code       json.RawMessage `json:"code"`
	Msg        string          `json:"msg"`
	TradeNo    string          `json:"trade_no"`
	OutTradeNo string          `json:"out_trade_no"`
	Type       string          `json:"type"`
	PID        json.RawMessage `json:"pid"`
	Name       string          `json:"name"`
	Money      string          `json:"money"`
	Status     json.RawMessage `json:"status"`
}

func (r queryResponse) IsSuccess() bool { return rawIsOne(r.Code) }

// StatusInt extracts the trade status (1=paid, 0=unpaid) from the loose
// JSON shape. Returns -1 on parse error.
func (r queryResponse) StatusInt() int {
	s := strings.Trim(strings.TrimSpace(string(r.Status)), `"`)
	switch s {
	case "1":
		return 1
	case "0":
		return 0
	}
	return -1
}

// rawIsOne returns true if a json.RawMessage encodes the literal 1 or "1".
func rawIsOne(raw json.RawMessage) bool {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	return s == "1"
}
