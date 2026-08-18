// Package growth implements marketing-channel attribution for the SaaS layer:
//
//   - per-channel referral links of the form  https://<site>/?ref=<slug>
//   - a configurable USD signup bonus granted to users who arrive via a channel
//   - anonymous visit / dwell-time tracking and conversion analytics, surfaced
//     in a dedicated admin-panel tab
//
// It is deliberately self-contained so it stays easy to maintain and lift out:
// it owns its own three tables (marketing_channels, channel_visits,
// channel_referrals — created by SaaS migration v5) and touches the rest of the
// codebase only through two narrow seams:
//
//   - a *sql.DB handle for its own tables, and
//   - a Wallet interface to credit the signup bonus through the audited
//     wallet ledger (so a bonus is recorded exactly like an admin gift).
//
// The package imports nothing from internal/saas/{db,auth,billing}; wiring
// happens in cmd/server/main.go and internal/saas/adapter.
package growth

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
)

// Wallet is the minimal slice of the wallet ledger growth needs: applying a
// signed credit to a user's balance. *saas/db.DB satisfies it. Keeping it an
// interface means growth never imports the db package and the bonus still flows
// through the same audited AddBalance path as an admin adjustment.
type Wallet interface {
	AddBalance(ctx context.Context, userID int64, kind string, deltaUSD float64, ref, note string, allowNegative bool) (newBal float64, err error)
}

// txKindBonus is the wallet_tx.kind value used for a channel signup bonus. It
// matches the ledger's "adjust" enum (an operator-style credit) rather than
// "topup" (paid money), so revenue reports don't count gifted credit as income.
const txKindBonus = "adjust"

// Service is the growth module. Construct with New and hold a single instance;
// it is safe for concurrent use (all state lives in SQLite).
type Service struct {
	db     *sql.DB
	wallet Wallet
	fraud  FraudConfig
	// suspended mirrors saas.referrals_enabled being off — see
	// referral.Service.Suspended for why the admin surface stays mounted.
	suspended bool
}

// SetSuspended records that the invite/channel programme is off, so the admin
// analytics payload can tell the operator their channel edits are inert.
func (s *Service) SetSuspended(v bool) { s.suspended = v }

// New builds the growth service over an open SQLite handle and a wallet sink.
// Anti-abuse runs with safe defaults (see defaultFraudConfig); call
// ConfigureFraud to override from operator config.
func New(db *sql.DB, wallet Wallet) *Service {
	return &Service{db: db, wallet: wallet, fraud: defaultFraudConfig()}
}

// ConfigureFraud overrides the signup anti-abuse policy. A zero-value field
// falls back to its default so a partially-specified config still behaves.
func (s *Service) ConfigureFraud(cfg FraudConfig) {
	def := defaultFraudConfig()
	s.fraud.Enabled = cfg.Enabled
	if cfg.SubnetThreshold > 0 {
		s.fraud.SubnetThreshold = cfg.SubnetThreshold
	} else {
		s.fraud.SubnetThreshold = def.SubnetThreshold
	}
	if cfg.Window > 0 {
		s.fraud.Window = cfg.Window
	} else {
		s.fraud.Window = def.Window
	}
	if cfg.EmailDomainThreshold > 0 {
		s.fraud.EmailDomainThreshold = cfg.EmailDomainThreshold
	} else {
		s.fraud.EmailDomainThreshold = def.EmailDomainThreshold
	}
	// RequireFingerprint has no "unset" sentinel of its own — main.go resolves
	// the operator's *bool (absent → default true) before calling us, exactly as
	// it already does for Enabled.
	s.fraud.RequireFingerprint = cfg.RequireFingerprint
	s.fraud.DisposableDomains = cfg.DisposableDomains
}

// slugRe constrains channel slugs to URL-safe, lowercase tokens so a ?ref=
// value can never carry markup, path traversal, or whitespace into storage.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,30}$`)

// NormalizeSlug lowercases/trims a candidate slug and returns it only if it is
// valid; the empty string signals "reject". Shared by the admin CRUD path (when
// creating a channel) and the public tracking path (when validating ?ref=).
func NormalizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if !slugRe.MatchString(s) {
		return ""
	}
	return s
}

// maxVisitorIDLen caps the anonymous visitor identifier the browser sends, so a
// malicious client can't bloat the row with an oversized id.
const maxVisitorIDLen = 64

// sanitizeVisitorID trims and length-bounds a client-supplied visitor id.
func sanitizeVisitorID(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxVisitorIDLen {
		s = s[:maxVisitorIDLen]
	}
	return s
}
