package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
)

func TestWriteAPIErrorUsesProviderCompatibleSchemas(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name, provider, codePath string
	}{
		{"openai", auth.ProviderOpenAI, "error.code"},
		{"anthropic", auth.ProviderAnthropic, "error.type"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			writeAPIError(c, tc.provider, APIError{Status: http.StatusTooManyRequests, Code: "api_key_monthly_limit_exceeded", Message: "Monthly limit reached.", Details: map[string]any{"limit_usd": 100}})
			if w.Code != http.StatusTooManyRequests || w.Header().Get("X-Error-Code") != "api_key_monthly_limit_exceeded" {
				t.Fatalf("status/header = %d/%q", w.Code, w.Header().Get("X-Error-Code"))
			}
			var got map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			errObj, ok := got["error"].(map[string]any)
			if !ok || (errObj["code"] != "api_key_monthly_limit_exceeded" && errObj["type"] != "api_key_monthly_limit_exceeded") {
				t.Fatalf("unexpected schema: %s", w.Body.String())
			}
		})
	}
}

func TestPublicUpstreamErrorRedactsImplementationDetails(t *testing.T) {
	body := []byte(`{"error":{"message":"relay.example balance depleted for credential acct-123"}}`)
	e := publicUpstreamError(http.StatusPaymentRequired, body)
	if e.Status != http.StatusServiceUnavailable || e.Code != "service_temporarily_unavailable" {
		t.Fatalf("unexpected mapping: %+v", e)
	}
	for _, secret := range []string{"relay.example", "balance", "credential", "acct-123"} {
		if strings.Contains(strings.ToLower(e.Message), secret) {
			t.Fatalf("public message leaked %q: %q", secret, e.Message)
		}
	}
}

func TestPublicUpstreamErrorExplainsContextLimit(t *testing.T) {
	e := publicUpstreamError(http.StatusTooManyRequests, []byte(`{"error":{"message":"context_length_exceeded"}}`))
	if e.Code != "context_length_exceeded" || !strings.Contains(strings.ToLower(e.Message), "too large") {
		t.Fatalf("unexpected mapping: %+v", e)
	}
}
