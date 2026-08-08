package referral

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/growth"
)

// newRealSvc wires referral against the REAL growth service rather than
// stubChannel, so the anti-abuse rules actually run. The stub short-circuits
// them to a fixed verdict, which is right for testing referral's own logic and
// useless for testing whether the two halves stop an attack together.
func newRealSvc(t *testing.T) (*Service, *db.DB) {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "farm.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	g := growth.New(store.DB, store)
	return New(store, nil, g, "Test", "http://localhost:8317"), store
}

// TestInviteFarmReplay2026_08_08 replays the actual attack against the fixed
// system. The farm's shape, taken from production:
//
//   - one inviter account minting an invite code
//   - 20 invitees registered through it in one day
//   - every one from its own Cloudflare WARP /24 (so the ip_subnet rule, which
//     needs 3 signups sharing a /24, can never fire)
//   - none carrying a browser fingerprint (they POSTed /auth/register directly,
//     so the fingerprint rule has nothing to match)
//   - addresses on domains the attacker controls
//
// Before the fix this paid the inviter $20 plus $16 of milestone tiers, with
// every conversion recorded fraud=0. The assertion here is the whole point of
// the incident response: this exact sequence must now yield the farm nothing.
func TestInviteFarmReplay2026_08_08(t *testing.T) {
	ctx := context.Background()
	svc, store := newRealSvc(t)

	inviter := mkUser(t, store, "farmer@gmail.com")
	card, err := svc.MintCard(ctx, inviter, "claude", "dark", "join", "")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	for i := 0; i < 20; i++ {
		email := fmt.Sprintf("bot%d@zendyhost.web.id", i)
		invitee := mkUser(t, store, email)
		// A distinct /24 per signup, exactly as WARP presented them.
		ip := fmt.Sprintf("104.28.%d.%d", 156+i, 10+i)
		if _, _, _, _, err := svc.GrantSignupBonus(ctx, invitee, card.Code, "", "", ip, email); err != nil {
			t.Fatalf("signup %d: %v", i, err)
		}
		// Fund each bot from outside and burn it. The farm is not limited to
		// spending the bonus it was denied, so the strict question is whether
		// real spend by a flagged invitee unlocks the inviter reward. It must
		// not: the signup was judged fraudulent, and money arriving later does
		// not un-judge it.
		credit(t, store, invitee, 2)
		spend(t, store, invitee, 1)
		svc.ReleaseInviterReward(ctx, invitee)
	}

	if got := bal(t, store, inviter); got != 0 {
		t.Fatalf("the farm still earned the inviter $%.2f — it must earn nothing", got)
	}
	// Every conversion must be recorded as fraud, or the milestone ladder and
	// the rewarded-invite cap would both still count them.
	var clean int
	_ = store.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM referral_conversions WHERE inviter_user_id = ? AND fraud = 0`, inviter).Scan(&clean)
	if clean != 0 {
		t.Fatalf("%d farm conversions recorded as clean", clean)
	}
	// And no invitee should have been credited either.
	var paid float64
	_ = store.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount_usd),0) FROM wallet_tx WHERE ref LIKE 'ref_invitee:%' OR ref LIKE 'ref_inviter:%' OR ref LIKE 'ref_tier:%'`).Scan(&paid)
	if paid != 0 {
		t.Fatalf("referral bonuses totalling $%.2f were paid to the farm", paid)
	}
}

// TestLegitimateInviteStillPays is the other half of the same guarantee: the
// rules above must not make the feature unusable for real users. A friend on
// gmail, with a browser fingerprint, from their own IP, who actually spends —
// that person's inviter still gets paid.
func TestLegitimateInviteStillPays(t *testing.T) {
	ctx := context.Background()
	svc, store := newRealSvc(t)

	inviter := mkUser(t, store, "real@gmail.com")
	card, _ := svc.MintCard(ctx, inviter, "claude", "dark", "", "")

	invitee := mkUser(t, store, "friend@gmail.com")
	bonus, _, matched, fraud, err := svc.GrantSignupBonus(
		ctx, invitee, card.Code, "", "fp-real-browser", "203.0.113.42", "friend@gmail.com")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if fraud || !matched {
		t.Fatalf("a legitimate invite was flagged: matched=%v fraud=%v", matched, fraud)
	}
	if bonus != 1 {
		t.Fatalf("invitee bonus: want 1, got %v", bonus)
	}
	// Inviter is paid once the friend genuinely uses the product.
	spend(t, store, invitee, 0.50)
	svc.ReleaseInviterReward(ctx, invitee)
	if got := bal(t, store, inviter); got != 1 {
		t.Fatalf("inviter reward after real spend: want 1, got %v", got)
	}
}

// TestVelocityCapStopsACleanFarm covers the residual case: an attacker who
// solves every detection signal — real browser fingerprints, residential IPs in
// unrelated /24s, mainstream mail domains, genuine spend on each invitee. No
// per-signup rule can see anything wrong. The daily cap is what bounds them.
func TestVelocityCapStopsACleanFarm(t *testing.T) {
	ctx := context.Background()
	svc, store := newRealSvc(t)

	inviter := mkUser(t, store, "sophisticated@gmail.com")
	card, _ := svc.MintCard(ctx, inviter, "claude", "dark", "", "")

	for i := 0; i < 20; i++ {
		email := fmt.Sprintf("person%d@gmail.com", i)
		invitee := mkUser(t, store, email)
		fp := fmt.Sprintf("fp-unique-%d", i)
		ip := fmt.Sprintf("198.51.%d.%d", 100+i, 7+i)
		if _, _, _, fraud, err := svc.GrantSignupBonus(ctx, invitee, card.Code, "", fp, ip, email); err != nil || fraud {
			t.Fatalf("clean signup %d: fraud=%v err=%v", i, fraud, err)
		}
		spend(t, store, invitee, 1)
		svc.ReleaseInviterReward(ctx, invitee)
	}

	camp, _ := svc.ActiveCampaign(ctx)
	// 5 paid invites ($1 each) + NOIR $0 at 1 + PLATINUM $2 at 3. RESERVE needs
	// 10 qualified invites and the cap stops the count at 5, so it never pays.
	want := float64(camp.DailyInviteCap) + 2
	if got := bal(t, store, inviter); got != want {
		t.Fatalf("undetectable farm earned $%.2f, cap should have held it to $%.2f", got, want)
	}
}
