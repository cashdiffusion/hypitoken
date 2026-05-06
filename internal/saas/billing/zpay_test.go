package billing

import (
	"crypto/md5"
	"encoding/hex"
	"testing"
)

// TestSignZPay_KnownVector hand-computes the expected signature so the
// implementation can't drift silently. Empty values and the sign /
// sign_type fields must be excluded; remaining keys sort ASCII ascending
// and the merchant key concatenates without a separator.
func TestSignZPay_KnownVector(t *testing.T) {
	params := map[string]string{
		"pid":          "20220715225121",
		"type":         "alipay",
		"out_trade_no": "20160806151343349",
		"notify_url":   "http://www.pay.com/notify_url.php",
		"name":         "iPhone17",
		"money":        "1.00",
		"clientip":     "192.168.1.100",
		// must be ignored:
		"sign":      "deadbeef",
		"sign_type": "MD5",
		"empty":     "",
	}
	const key = "89unJUB8HZ54Hj7x4nUj56HN4nUzUJ8i"

	// Expected: hand-build the sorted concatenation and md5.
	want := md5sum("clientip=192.168.1.100" +
		"&money=1.00" +
		"&name=iPhone17" +
		"&notify_url=http://www.pay.com/notify_url.php" +
		"&out_trade_no=20160806151343349" +
		"&pid=20220715225121" +
		"&type=alipay" +
		key)

	got := SignZPay(params, key)
	if got != want {
		t.Fatalf("SignZPay mismatch\n got=%s\nwant=%s", got, want)
	}
}

// TestSignZPay_RoundTrip — sign a request, then verify the signature
// parses back. Catches encoding/sorting bugs both directions.
func TestSignZPay_RoundTrip(t *testing.T) {
	const key = "test-key-do-not-use-in-prod"
	params := map[string]string{
		"pid":          "9999",
		"out_trade_no": "TEST-1",
		"trade_no":     "ZP-1",
		"trade_status": "TRADE_SUCCESS",
		"money":        "0.01",
		"name":         "Test",
		"type":         "alipay",
	}
	sig := SignZPay(params, key)

	g := &ZPayGateway{PID: "9999", Key: key, BaseURL: "https://zpayz.cn"}
	form := map[string][]string{
		"sign":      {sig},
		"sign_type": {"MD5"},
	}
	for k, v := range params {
		form[k] = []string{v}
	}
	n, err := g.VerifyNotify(form)
	if err != nil {
		t.Fatalf("VerifyNotify: %v", err)
	}
	if n.OutTradeNo != "TEST-1" || n.TradeStatus != "TRADE_SUCCESS" || n.AppID != "9999" {
		t.Fatalf("notify mismatch: %+v", n)
	}
}

// TestSignZPay_Tampered — flip a value, expect rejection.
func TestSignZPay_Tampered(t *testing.T) {
	const key = "k"
	good := map[string]string{
		"pid": "1", "out_trade_no": "X", "money": "5.00",
	}
	sig := SignZPay(good, key)
	// Change `money` after signing — verifier must reject.
	form := map[string][]string{
		"pid":          {"1"},
		"out_trade_no": {"X"},
		"money":        {"500.00"},
		"sign":         {sig},
	}
	g := &ZPayGateway{PID: "1", Key: key}
	if _, err := g.VerifyNotify(form); err == nil {
		t.Fatal("expected signature mismatch error, got nil")
	}
}

func md5sum(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
