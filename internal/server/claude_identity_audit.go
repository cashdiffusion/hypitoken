package server

import (
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/requestlog"
)

// claudeIdentityAudit turns the opaque prepared-request result into
// privacy-safe request-log evidence. It never receives or stores a raw prompt,
// bearer, client token, account UUID, or email.
func claudeIdentityAudit(prepared mimicry.BodyTransformResult, accountKey string) *requestlog.ClaudeAudit {
	if !prepared.IsValid() || !prepared.AccountIdentityApplied() {
		return nil
	}
	mode := prepared.Policy().GenuineMode().String()
	if prepared.Policy().Class() == mimicry.RequestClassGeneric {
		mode = prepared.Policy().GenericMode().String()
	}
	return &requestlog.ClaudeAudit{
		AccountHash:           mimicry.HashClaudeAccountKey(accountKey),
		RequestClass:          prepared.Policy().Class().String(),
		IdentityMode:          mode,
		AccountIdentityMapped: prepared.AccountIdentityApplied(),
	}
}

func claudePreparationFailureAudit(requestClass mimicry.RequestClass, accountKey, reason string) *requestlog.ClaudeAudit {
	mode := "rewrite"
	if requestClass == mimicry.RequestClassGeneric {
		mode = mimicry.GenericRequestSynthesize.String()
	}
	return &requestlog.ClaudeAudit{
		AccountHash:       mimicry.HashClaudeAccountKey(accountKey),
		RequestClass:      requestClass.String(),
		IdentityMode:      mode,
		PreparationFailed: true,
		PreparationError:  reason,
		Fallback:          "apikey",
	}
}
