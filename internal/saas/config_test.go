package saas

import "testing"

// The welcome-credit and invite/referral programmes were farmed for signup
// credit (2026-08-08: 168 throwaway signups, ~$116 granted) and are suspended.
// The suspension has to hold on the binary alone — a production config.yaml
// that predates the shutdown must not resurrect either one — so both defaults
// are asserted here.
func TestApplyDefaultsSuspendsSignupBonusAndReferrals(t *testing.T) {
	c := &Config{}
	c.ApplyDefaults(t.TempDir())

	if c.SignupBonusUSD == nil || *c.SignupBonusUSD != 0 {
		t.Fatalf("signup bonus default = %v, want 0", c.SignupBonusUSD)
	}
	if c.ReferralsEnabled == nil || *c.ReferralsEnabled {
		t.Fatalf("referrals default = %v, want false", c.ReferralsEnabled)
	}
}

// The keys stay honoured, so an operator can turn either programme back on
// deliberately without a code change.
func TestApplyDefaultsKeepsExplicitOptIn(t *testing.T) {
	bonus := 1.0
	on := true
	c := &Config{SignupBonusUSD: &bonus, ReferralsEnabled: &on}
	c.ApplyDefaults(t.TempDir())

	if c.SignupBonusUSD == nil || *c.SignupBonusUSD != 1.0 {
		t.Fatalf("explicit signup bonus = %v, want 1", c.SignupBonusUSD)
	}
	if c.ReferralsEnabled == nil || !*c.ReferralsEnabled {
		t.Fatalf("explicit referrals = %v, want true", c.ReferralsEnabled)
	}
}

// The rollout case that actually bites: a config.yaml written before the
// shutdown still carries signup_bonus_usd, and nobody edits it. referrals_
// enabled is a newer key, so it is absent there and defaults false — which is
// what cmd/server gates the grant on, leaving the credit dormant regardless of
// the stale amount.
func TestApplyDefaultsLegacyBonusConfigStaysSuspended(t *testing.T) {
	bonus := 1.0
	c := &Config{SignupBonusUSD: &bonus} // pre-shutdown production config
	c.ApplyDefaults(t.TempDir())

	if c.ReferralsEnabled == nil || *c.ReferralsEnabled {
		t.Fatalf("referrals = %v, want false so the stale bonus stays ungranted", c.ReferralsEnabled)
	}
}
