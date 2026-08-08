package support

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsLikelyTaxNo(t *testing.T) {
	valid := []string{
		"91440300MA5EYQ8L2K", // 18-char 统一社会信用代码
		"440301123456789",    // 15-char legacy 税务登记号
		"91110108MA01ABCDEF",
	}
	for _, s := range valid {
		if !isLikelyTaxNo(s) {
			t.Errorf("want valid: %q", s)
		}
	}
	invalid := []string{
		"",
		"12345",                    // too short
		"91440300ma5eyq8l2k",       // lowercase — the handler upper-cases first
		"91440300 MA5EYQ8L2K",      // space
		"91440300-MA5EYQ8L2K",      // punctuation
		"914403001234567890123456", // too long
	}
	for _, s := range invalid {
		if isLikelyTaxNo(s) {
			t.Errorf("want invalid: %q", s)
		}
	}
}

// TestFetchRemoteSuggestParsesProviderShapes covers the three response shapes
// the suggest endpoints return, plus the rate-limit body — which arrives as
// HTTP 200 with an error code inside, so treating it as success would surface
// an empty list as if the company genuinely didn't exist.
func TestFetchRemoteSuggestParsesProviderShapes(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantName  string
		wantTaxNo string
		wantErr   bool
	}{
		{
			name:      "tianyancha bare list",
			body:      `{"state":"ok","errorCode":0,"data":[{"comName":"深圳<em>卡希涌流</em>科技有限公司","taxCode":"91440300MA5EYQ8L2K"}]}`,
			wantName:  "深圳卡希涌流科技有限公司",
			wantTaxNo: "91440300MA5EYQ8L2K",
		},
		{
			name:      "wrapped queryList",
			body:      `{"state":"ok","errorCode":0,"data":{"queryList":[{"entName":"某某有限公司","creditCode":"91110108MA01ABCDEF"}]}}`,
			wantName:  "某某有限公司",
			wantTaxNo: "91110108MA01ABCDEF",
		},
		{
			name:    "rate limited arrives as HTTP 200",
			body:    `{"state":"error","errorCode":302004,"message":"请登录","data":{"token":""}}`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			svc := &Service{titleSuggestURL: srv.URL, httpClient: srv.Client()}
			// Vary the keyword per case so one case cannot read another's cache.
			rows, err := svc.fetchRemoteSuggest(context.Background(), tc.name)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error for a rate-limited response, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("want 1 row, got %d", len(rows))
			}
			if rows[0].Name != tc.wantName {
				t.Errorf("name: want %q, got %q", tc.wantName, rows[0].Name)
			}
			if rows[0].TaxNo != tc.wantTaxNo {
				t.Errorf("tax no: want %q, got %q", tc.wantTaxNo, rows[0].TaxNo)
			}
		})
	}
}

// TestSuggestCacheStretchesTheUpstreamBudget: the upstream is rate-limited per
// source IP and shared by every customer, so a repeated keyword must cost one
// call, not one per keystroke.
func TestSuggestCacheStretchesTheUpstreamBudget(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"state":"ok","errorCode":0,"data":[{"comName":"缓存公司","taxCode":"91440300MA5EYQ8L2K"}]}`))
	}))
	defer srv.Close()

	svc := &Service{titleSuggestURL: srv.URL, httpClient: srv.Client()}
	for range 3 {
		if _, err := svc.fetchRemoteSuggest(context.Background(), "cache-budget-keyword"); err != nil {
			t.Fatalf("fetch: %v", err)
		}
	}
	if hits != 1 {
		t.Fatalf("want 1 upstream call for 3 identical lookups, got %d", hits)
	}
}

// TestInvoiceTicketCarriesStructuredTitle is the guarantee the operator relies
// on: the 抬头 survives as machine-readable fields (so the panel renders
// copyable values) AND as prose in the body (so a plain-text read of the thread
// still contains everything).
func TestInvoiceTicketCarriesStructuredTitle(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)

	title := InvoiceTitle{
		Name:    "深圳卡希涌流科技有限公司",
		TaxNo:   "91440300MA5EYQ8L2K",
		Address: "深圳市南山区",
	}
	meta := invoiceMeta{Kind: KindInvoice, Title: title, AmountCNY: 1000, Note: "季度对账"}
	raw, _ := json.Marshal(meta)

	tk, err := svc.CreateWithMeta(ctx, 7, "cfo@example.com", KindInvoice,
		"开票申请 · "+title.Name, invoiceBody(meta), string(raw))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if tk.Kind != KindInvoice {
		t.Fatalf("kind: want invoice, got %s", tk.Kind)
	}

	got, err := svc.Get(ctx, tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var back invoiceMeta
	if err := json.Unmarshal([]byte(got.Meta), &back); err != nil {
		t.Fatalf("meta round-trip: %v", err)
	}
	if back.Title.TaxNo != title.TaxNo || back.Title.Name != title.Name {
		t.Fatalf("title lost in round-trip: %+v", back.Title)
	}
	if back.AmountCNY != 1000 {
		t.Fatalf("amount: want 1000, got %v", back.AmountCNY)
	}
	// The prose copy must carry the same tax number, or a text-only read of the
	// ticket would be missing the one field that must be exact.
	if !strings.Contains(got.Messages[0].Body, title.TaxNo) {
		t.Fatalf("body is missing the tax number: %q", got.Messages[0].Body)
	}
}

// TestPaymentInfoOverride: the built-in account works with no config, and a
// partial override changes only what it names.
func TestPaymentInfoOverride(t *testing.T) {
	svc := newSvc(t)
	if svc.payment.AccountNo != "15000593710093" {
		t.Fatalf("default account: got %q", svc.payment.AccountNo)
	}
	svc.ConfigureInvoicing("", "", PaymentInfo{AccountName: "新主体"})
	if svc.payment.AccountName != "新主体" {
		t.Fatalf("override ignored: %q", svc.payment.AccountName)
	}
	if svc.payment.AccountNo != "15000593710093" {
		t.Fatalf("partial override clobbered the account number: %q", svc.payment.AccountNo)
	}
	if svc.titleSuggestURL != defaultTitleSuggestURL {
		t.Fatalf("empty suggest URL should keep the default, got %q", svc.titleSuggestURL)
	}
}

// TestConfigureInvoicingProxy: the default provider blocks offshore IPs, so an
// egress proxy is the difference between a working picker and a dead one in
// production. A malformed value must not take the client down with it.
func TestConfigureInvoicingProxy(t *testing.T) {
	svc := newSvc(t)
	direct := svc.httpClient

	svc.ConfigureInvoicing("", "socks5://127.0.0.1:1080", PaymentInfo{})
	if svc.httpClient == direct {
		t.Fatal("proxy config did not replace the http client")
	}
	if svc.httpClient.Transport == nil {
		t.Fatal("proxied client has no transport")
	}

	// An unparseable proxy leaves the previous client in place rather than
	// nil-ing it out — a bad config line must degrade the lookup, not panic it.
	prev := svc.httpClient
	svc.ConfigureInvoicing("", "://nonsense", PaymentInfo{})
	if svc.httpClient != prev {
		t.Fatal("a malformed proxy must leave the working client alone")
	}
}
