package server

import (
	"bufio"
	"bytes"
	"net/http"
	"strings"
)

// upstreamFault classifies why an upstream exchange did not produce a usable
// response. The distinction drives two separate decisions that used to be
// conflated in a single `default: MarkFailure()` branch:
//
//   - whether the failure counts against the credential's health (only faults
//     the credential or the upstream is responsible for should), and
//   - whether retrying the same request on a *different* credential can
//     plausibly help (it cannot when the client's own request is malformed).
//
// This is the standard API-gateway split between client-side and server-side
// errors: counting a client's 400 toward a circuit breaker lets one broken
// caller trip the breaker for every other caller sharing the channel.
type upstreamFault int

const (
	// faultNone — the exchange succeeded and satisfied the response contract.
	faultNone upstreamFault = iota

	// faultClient — the caller's own request is at fault (malformed body,
	// unsupported route, payload too large). Every credential would return
	// the same thing, so this is forwarded verbatim, is never retried, and
	// never touches credential health.
	faultClient

	// faultUpstream — the upstream service, the relay in front of it, or the
	// network between us failed: transport errors, throttling, gateway
	// errors, and responses that violate the API contract (see
	// validateAnthropicResponse). Counts toward health and retries on another
	// credential, but is not a verdict on this credential's validity.
	faultUpstream

	// faultCredential — this specific credential is not usable: revoked,
	// forbidden, or out of funds. Counts toward health with the heaviest
	// weight and retries on another credential.
	faultCredential
)

func (f upstreamFault) retryable() bool {
	return f == faultUpstream || f == faultCredential
}

// classifyUpstreamStatus maps an upstream HTTP status onto an upstreamFault.
//
// The 4xx range is deliberately split rather than treated as one bucket. A
// 400 (bad request body), 404 (route not implemented by this relay), 413
// (payload too large) or 422 (unprocessable) are properties of the *request*
// — retrying them on another credential burns an upstream round-trip to
// arrive at the identical error, and counting them toward credential health
// lets a single misbehaving client degrade a channel that is working
// perfectly for everyone else.
//
// 408 and 429 are the exceptions inside 4xx: both describe the upstream's
// current condition (timed out, throttling) rather than a defect in the
// request, so they classify as faultUpstream and do retry.
//
// Anything unrecognised in 4xx is treated as faultClient — the conservative
// direction, since it neither burns retries nor pollutes health state.
func classifyUpstreamStatus(status int) upstreamFault {
	switch {
	case status < 400:
		return faultNone
	case status == http.StatusUnauthorized,
		status == http.StatusPaymentRequired,
		status == http.StatusForbidden:
		return faultCredential
	case status == http.StatusRequestTimeout,
		status == http.StatusTooManyRequests:
		return faultUpstream
	case status >= 500:
		return faultUpstream
	default:
		return faultClient
	}
}

// contractViolation describes a 2xx/3xx response whose body does not conform
// to the Anthropic Messages API wire format. Empty Detail means no violation.
type contractViolation struct {
	Detail string
}

// validateAnthropicResponse checks that a non-error response actually carries
// an Anthropic-shaped payload before we commit it to the client and bill it.
//
// Status codes alone are not a health signal for a gateway. A dead or
// misconfigured relay in front of api.anthropic.com typically answers
// `200 OK` with an HTML interstitial ("Access restricted", a login wall, a
// CDN block page). Trusting the status code makes the proxy mark the
// credential healthy, stream an empty response to the client, and bill zero
// tokens — a silent failure that is only visible afterwards by noticing the
// credential logged nothing but zero-token rows.
//
// The check is deliberately shape-based, not Content-Type-based: relays are
// known to stream a perfectly valid SSE body under `text/plain` (the Codex
// path already compensates for exactly this). So a response passes when its
// first bytes look like either an Anthropic JSON object or an SSE field
// line, and fails otherwise. That is narrow enough to admit every legitimate
// upstream — including quirky relays — while still rejecting the HTML pages
// that caused the silent-failure incident.
//
// Peeking is non-consuming, so the same buffered reader is handed to the SSE
// relay or the whole-body read afterwards.
func validateAnthropicResponse(h http.Header, br *bufio.Reader) contractViolation {
	if looksLikeSSE(br) || looksLikeJSONObject(br) {
		return contractViolation{}
	}
	peek, _ := br.Peek(256)
	if len(peek) == 0 {
		// A 200 with a completely empty body carries no message and no usage.
		// Treat it as a failed exchange rather than a free, zero-token success.
		return contractViolation{Detail: "empty body"}
	}
	ct := h.Get("Content-Type")
	if ct == "" {
		ct = "(none)"
	}
	return contractViolation{
		Detail: "content-type=" + ct + " body=" + strings.TrimSpace(string(peek)),
	}
}

// looksLikeJSONObject peeks the head of a buffered reader and reports whether
// the first non-whitespace byte opens a JSON object. Non-consuming.
//
// Only `{` counts: every Anthropic Messages response — success or error — is
// an object. A bare array, string, or number would be as much of a contract
// violation as HTML.
func looksLikeJSONObject(br *bufio.Reader) bool {
	peek, _ := br.Peek(512)
	trimmed := bytes.TrimLeft(peek, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == '{'
}
