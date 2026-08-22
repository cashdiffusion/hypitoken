package server

// MaskClientToken applies the request log's client-token masking rule.
//
// Exported so tools that have to read the request log back — reconciliation,
// audits — can map a masked value in `req.client_token` to the secret it came
// from without restating the rule. A second copy of it would silently stop
// matching the day this one changed, and the failure mode is a reconciliation
// that reports every token as unresolvable, which is what the first production
// run of `reconcile-charges` actually did.
func MaskClientToken(t string) string { return maskClientToken(t) }
