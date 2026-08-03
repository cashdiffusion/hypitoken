package server

import (
	"net/http"

	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
)

// claudeRewriteAudit turns the opaque prepared-request result and Anthropic's
// response header into privacy-safe request-log evidence. It never receives or
// stores a raw prompt, bearer, client token, cc_prev_req, or response request-id.
func claudeRewriteAudit(prepared mimicry.BodyTransformResult, responseHeader http.Header, accountKey string) *requestlog.ClaudeAudit {
	if !prepared.IsValid() ||
		prepared.Policy().Class() != mimicry.RequestClassGenuine ||
		prepared.Policy().GenuineMode() != mimicry.GenuineRequestRewriteStripCCH {
		return nil
	}
	return &requestlog.ClaudeAudit{
		AccountHash:           mimicry.HashClaudeAccountKey(accountKey),
		RequestClass:          prepared.Policy().Class().String(),
		IdentityMode:          prepared.Policy().GenuineMode().String(),
		AccountIdentityMapped: prepared.AccountIdentityApplied(),
		CCHStripped:           prepared.CCHStripped(),
		CCPrevReqPresent:      prepared.CCPrevReqPresent(),
		CCPrevReqPreserved:    prepared.CCPrevReqPreserved(),
		CCPrevReqHash:         prepared.CCPrevReqHash(),
		ResponseRequestIDHash: mimicry.HashClaudeRequestID(responseHeader.Get("Request-Id")),
	}
}
