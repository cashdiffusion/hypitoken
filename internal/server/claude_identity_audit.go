package server

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
)

const maxAuditNames = 16

// claudeIdentityAudit turns the opaque prepared-request result into
// privacy-safe request-log evidence. It never receives or stores a raw prompt,
// bearer, client token, account UUID, or email.
func claudeIdentityAudit(prepared mimicry.BodyTransformResult, accountKey, clientToken string, headers http.Header, proxyURL string) *requestlog.ClaudeAudit {
	if !prepared.IsValid() || !prepared.AccountIdentityApplied() {
		return nil
	}
	mode := prepared.Policy().GenuineMode().String()
	if prepared.Policy().Class() == mimicry.RequestClassGeneric {
		mode = prepared.Policy().GenericMode().String()
	}
	metadataKeys := prepared.ExtraMetadataKeys()
	extraHeaders := unexpectedClaudeHeaderNames(headers)
	audit := &requestlog.ClaudeAudit{
		AccountHash:           mimicry.HashClaudeAccountKey(accountKey),
		ClientHash:            hashAuditValues("claude-client/v1", clientToken),
		RequestClass:          prepared.Policy().Class().String(),
		IdentityMode:          mode,
		AccountIdentityMapped: prepared.AccountIdentityApplied(),
		BodyBytes:             prepared.BodyBytes(),
		BodySHA256:            prepared.BodySHA256(),
		SessionBinding:        sessionBinding(prepared.SessionID(), headers.Get("X-Claude-Code-Session-Id")),
		BillingValidation:     validationStatus(prepared.BillingVerified()),
		BetaHash:              hashAuditValues("claude-beta/v1", headers.Get("Anthropic-Beta")),
		ProfileHash:           claudeProfileHash(headers),
		ProxyConfigHash:       proxyConfigHash(proxyURL),
		ExtraMetadataCount:    len(metadataKeys),
		ExtraHeaderCount:      len(extraHeaders),
	}
	audit.ExtraMetadataKeys = cappedAuditNames(metadataKeys)
	audit.ExtraHeaderNames = cappedAuditNames(extraHeaders)
	return audit
}

func claudePreparationFailureAudit(requestClass mimicry.RequestClass, accountKey, clientToken, reason, fallback string, failures int, body []byte, headers http.Header, proxyURL string) *requestlog.ClaudeAudit {
	mode := "rewrite"
	if requestClass == mimicry.RequestClassGeneric {
		mode = mimicry.GenericRequestSynthesize.String()
	}
	return &requestlog.ClaudeAudit{
		AccountHash:         mimicry.HashClaudeAccountKey(accountKey),
		ClientHash:          hashAuditValues("claude-client/v1", clientToken),
		RequestClass:        requestClass.String(),
		IdentityMode:        mode,
		PreparationFailed:   true,
		PreparationError:    reason,
		PreparationFailures: failures,
		Fallback:            fallback,
		BodyBytes:           len(body),
		BodySHA256:          hashBody(body),
		SessionBinding:      "unavailable",
		BillingValidation:   "failed",
		BetaHash:            hashAuditValues("claude-beta/v1", headers.Get("Anthropic-Beta")),
		ProfileHash:         claudeProfileHash(headers),
		ProxyConfigHash:     proxyConfigHash(proxyURL),
	}
}

func hashBody(body []byte) string {
	return mimicry.ClaudeRequestStructureSHA256(body)
}

func hashAuditValues(domain string, values ...string) string {
	if len(values) == 0 {
		return ""
	}
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0})
	for _, value := range values {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil)[:8])
}

func sessionBinding(bodySession, headerSession string) string {
	if bodySession == "" || headerSession == "" {
		return "unavailable"
	}
	if bodySession == headerSession {
		return "match"
	}
	return "mismatch"
}

func validationStatus(ok bool) string {
	if ok {
		return "verified"
	}
	return "unavailable"
}

func claudeProfileHash(headers http.Header) string {
	if len(headers) == 0 {
		return ""
	}
	keys := []string{
		"User-Agent", "Anthropic-Version", "X-App", "X-Stainless-Retry-Count",
		"X-Stainless-Lang", "X-Stainless-Runtime", "X-Stainless-Runtime-Version",
		"X-Stainless-Package-Version", "X-Stainless-Os", "X-Stainless-Arch", "X-Stainless-Timeout",
	}
	values := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		values = append(values, strings.ToLower(key), headers.Get(key))
	}
	return hashAuditValues("claude-profile/v1", values...)
}

func proxyConfigHash(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return hashAuditValues("claude-proxy/v1", "invalid-config")
	}
	endpoint := strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
	return hashAuditValues("claude-proxy/v1", endpoint)
}

func unexpectedClaudeHeaderNames(headers http.Header) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		lower := strings.ToLower(name)
		if expectedClaudeHeader(lower) {
			continue
		}
		names = append(names, lower)
	}
	sort.Strings(names)
	return names
}

func expectedClaudeHeader(name string) bool {
	if strings.HasPrefix(name, "x-stainless-") {
		return true
	}
	switch name {
	case "accept", "accept-encoding", "anthropic-beta", "anthropic-dangerous-direct-browser-access",
		"anthropic-version", "authorization", "connection", "content-type", "user-agent", "x-api-key",
		"x-app", "x-claude-code-session-id", "x-client-request-id":
		return true
	default:
		return false
	}
}

func cappedAuditNames(names []string) []string {
	if len(names) > maxAuditNames {
		names = names[:maxAuditNames]
	}
	return append([]string(nil), names...)
}
