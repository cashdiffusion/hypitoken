package server

import (
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
)

// claudeIdentityAudit turns the opaque prepared-request result into
// privacy-safe request-log evidence. It never receives or stores a raw prompt,
// bearer, client token, account UUID, or email.
func claudeIdentityAudit(prepared mimicry.BodyTransformResult, accountKey string) *requestlog.ClaudeAudit {
	if !prepared.IsValid() ||
		prepared.Policy().Class() != mimicry.RequestClassGenuine ||
		prepared.Policy().GenuineMode() != mimicry.GenuineRequestRewrite {
		return nil
	}
	return &requestlog.ClaudeAudit{
		AccountHash:           mimicry.HashClaudeAccountKey(accountKey),
		RequestClass:          prepared.Policy().Class().String(),
		IdentityMode:          prepared.Policy().GenuineMode().String(),
		AccountIdentityMapped: prepared.AccountIdentityApplied(),
	}
}
