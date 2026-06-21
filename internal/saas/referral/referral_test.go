package referral

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// stubChannel satisfies ChannelGranter. fraud controls the anti-abuse verdict;
// the channel-fallback path returns "no channel".
type stubChannel struct{ fraud bool }

func (s stubChannel) RecordSignupDevice(_ context.Context, _ int64, _, _ string) (bool, string, error) {
	return s.fraud, "", nil
}

func (s stubChannel) GrantSignupBonus(_ context.Context, _ int64, _, _, _, _ string) (float64, string, bool, bool, error) {
	return 0, "", false, s.fraud, nil
}

func newSvc(t *testing.T, fraud bool) (*Service, *db.DB) {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return New(store, nil, stubChannel{fraud: fraud}, "Test", "http://localhost:8317"), store
}

func mkUser(t *testing.T, store *db.DB, email string) int64 {
	t.Helper()
	u, err := store.CreateUser(context.Background(), email, "hash", "user", 1, true)
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	return u.ID
}

func credit(t *testing.T, store *db.DB, userID int64, amt float64) {
	t.Helper()
	if _, err := store.AddBalance(context.Background(), userID, db.TxKindTopup, amt, "test", "", false); err != nil {
		t.Fatalf("credit: %v", err)
	}
}

func bal(t *testing.T, store *db.DB, userID int64) float64 {
	t.Helper()
	b, err := store.GetBalance(context.Background(), userID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	return b
}

func TestInviteTwoSidedGrant(t *testing.T) {
	ctx := context.Background()
	svc, store := newSvc(t, false)
	inviter := mkUser(t, store, "inviter@test.com")
	card, err := svc.MintCard(ctx, inviter, "claude", "dark", "join me", "")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	invitee := mkUser(t, store, "newbie@test.com")

	bonus, _, matched, fraud, err := svc.GrantSignupBonus(ctx, invitee, card.Code, "", "fp1", "1.2.3.4")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if !matched || fraud {
		t.Fatalf("want matched && !fraud, got matched=%v fraud=%v", matched, fraud)
	}
	if bonus != 1 {
		t.Fatalf("invitee bonus: want 1, got %v", bonus)
	}
	if got := bal(t, store, invitee); got != 1 {
		t.Fatalf("invitee balance: want 1, got %v", got)
	}
	if got := bal(t, store, inviter); got != 1 {
		t.Fatalf("inviter balance: want 1, got %v", got)
	}
	// lifetime_invites bumped + a conversion recorded (idempotent on replay).
	if n := svc.countConfirmedInvites(ctx, inviter); n != 1 {
		t.Fatalf("confirmed invites: want 1, got %d", n)
	}
	// Replaying the same invitee must not double-credit.
	if _, _, _, _, err := svc.GrantSignupBonus(ctx, invitee, card.Code, "", "fp1", "1.2.3.4"); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got := bal(t, store, inviter); got != 1 {
		t.Fatalf("inviter balance after replay: want 1, got %v", got)
	}
}

func TestInviteFraudWithheld(t *testing.T) {
	ctx := context.Background()
	svc, store := newSvc(t, true) // anti-abuse flags every signup
	inviter := mkUser(t, store, "inviter@test.com")
	card, _ := svc.MintCard(ctx, inviter, "claude", "dark", "", "")
	invitee := mkUser(t, store, "farm@test.com")

	bonus, _, matched, fraud, err := svc.GrantSignupBonus(ctx, invitee, card.Code, "", "fp", "ip")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if !matched || !fraud {
		t.Fatalf("want matched && fraud, got matched=%v fraud=%v", matched, fraud)
	}
	if bonus != 0 {
		t.Fatalf("fraud invitee bonus: want 0, got %v", bonus)
	}
	if got := bal(t, store, inviter); got != 0 {
		t.Fatalf("inviter balance on fraud: want 0, got %v", got)
	}
}

func TestMilestoneSingleGrant(t *testing.T) {
	ctx := context.Background()
	svc, store := newSvc(t, false)
	inviter := mkUser(t, store, "inviter@test.com")
	card, _ := svc.MintCard(ctx, inviter, "claude", "dark", "", "")

	// Default campaign tiers: 1 NOIR ($0), 3 PLATINUM ($2). Invite 3 → +$2 once.
	for i := 0; i < 3; i++ {
		invitee := mkUser(t, store, "u"+string(rune('a'+i))+"@test.com")
		if _, _, _, _, err := svc.GrantSignupBonus(ctx, invitee, card.Code, "", "fp", ""); err != nil {
			t.Fatalf("grant %d: %v", i, err)
		}
	}
	// 3 invitee bonuses ($1 each) + 3 inviter bonuses ($1) + 1 milestone ($2).
	if got := bal(t, store, inviter); got != 5 {
		t.Fatalf("inviter balance after 3 invites: want 5 (3×$1 + $2 tier), got %v", got)
	}
	// Re-checking milestones must not re-pay.
	camp, _ := svc.ActiveCampaign(ctx)
	svc.checkMilestones(ctx, inviter, camp)
	if got := bal(t, store, inviter); got != 5 {
		t.Fatalf("milestone re-grant leaked: want 5, got %v", got)
	}
}

func TestGiftSendClaimConservation(t *testing.T) {
	ctx := context.Background()
	svc, store := newSvc(t, false)
	sender := mkUser(t, store, "sender@test.com")
	credit(t, store, sender, 10)

	// Gift to an email with no account yet → escrow pending, sender debited.
	gift, err := svc.SendGift(ctx, sender, "sender@test.com", "friend@test.com", 3, "enjoy", "openai", "dark")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if gift.Status != db.GiftPending {
		t.Fatalf("status: want pending, got %s", gift.Status)
	}
	if got := bal(t, store, sender); got != 7 {
		t.Fatalf("sender balance after send: want 7, got %v", got)
	}

	// Recipient registers + claims.
	recipient := mkUser(t, store, "friend@test.com")
	claimed, total := svc.AutoClaimForEmail(ctx, "friend@test.com", recipient)
	if claimed != 1 || total != 3 {
		t.Fatalf("auto-claim: want (1,3), got (%d,%v)", claimed, total)
	}
	if got := bal(t, store, recipient); got != 3 {
		t.Fatalf("recipient balance: want 3, got %v", got)
	}
	// Conservation: sender(7) + recipient(3) == initial 10.
	if got := bal(t, store, sender) + bal(t, store, recipient); got != 10 {
		t.Fatalf("conservation: want 10, got %v", got)
	}
	// Double-claim is a benign no-op.
	if c2, _ := svc.AutoClaimForEmail(ctx, "friend@test.com", recipient); c2 != 0 {
		t.Fatalf("double auto-claim: want 0, got %d", c2)
	}
}

func TestGiftImmediateClaimForExistingUser(t *testing.T) {
	ctx := context.Background()
	svc, store := newSvc(t, false)
	sender := mkUser(t, store, "sender@test.com")
	credit(t, store, sender, 10)
	recipient := mkUser(t, store, "friend@test.com")

	gift, err := svc.SendGift(ctx, sender, "sender@test.com", "friend@test.com", 4, "", "claude", "dark")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if gift.Status != db.GiftClaimed {
		t.Fatalf("existing-user gift should claim immediately, got %s", gift.Status)
	}
	if got := bal(t, store, recipient); got != 4 {
		t.Fatalf("recipient balance: want 4, got %v", got)
	}
}

func TestGiftValidation(t *testing.T) {
	ctx := context.Background()
	svc, store := newSvc(t, false)
	sender := mkUser(t, store, "sender@test.com")
	credit(t, store, sender, 5)

	if _, err := svc.SendGift(ctx, sender, "sender@test.com", "sender@test.com", 1, "", "claude", "dark"); !errors.Is(err, ErrSelfGift) {
		t.Fatalf("self-gift: want ErrSelfGift, got %v", err)
	}
	if _, err := svc.SendGift(ctx, sender, "sender@test.com", "x@test.com", 0, "", "claude", "dark"); !errors.Is(err, ErrAmountTooSmall) {
		t.Fatalf("zero amount: want ErrAmountTooSmall, got %v", err)
	}
	if _, err := svc.SendGift(ctx, sender, "sender@test.com", "x@test.com", 9999, "", "claude", "dark"); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("over cap: want ErrAmountTooLarge, got %v", err)
	}
	if _, err := svc.SendGift(ctx, sender, "sender@test.com", "x@test.com", 50, "", "claude", "dark"); !errors.Is(err, db.ErrInsufficientBalance) {
		t.Fatalf("insufficient: want ErrInsufficientBalance, got %v", err)
	}
}

func TestGiftExpirySweep(t *testing.T) {
	ctx := context.Background()
	svc, store := newSvc(t, false)
	sender := mkUser(t, store, "sender@test.com")
	credit(t, store, sender, 10)

	gift, err := svc.SendGift(ctx, sender, "sender@test.com", "ghost@test.com", 6, "", "claude", "dark")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := bal(t, store, sender); got != 4 {
		t.Fatalf("after send: want 4, got %v", got)
	}
	// Force-expire the gift in the past, then sweep.
	if _, err := store.ExecContext(ctx, `UPDATE gift_cards SET expires_at = ? WHERE code = ?`, time.Now().Add(-time.Hour).Unix(), gift.Code); err != nil {
		t.Fatalf("expire: %v", err)
	}
	svc.sweepExpiredGifts(ctx)
	if got := bal(t, store, sender); got != 10 {
		t.Fatalf("after refund: want 10, got %v", got)
	}
	g, _ := store.GetGiftByCode(ctx, gift.Code)
	if g.Status != db.GiftRefunded {
		t.Fatalf("status: want refunded, got %s", g.Status)
	}
}

func TestCodeFormatting(t *testing.T) {
	if got := formatRedeem("HYPI4F2K9J3M"); got != "HYPI-4F2K-9J3M" {
		t.Fatalf("formatRedeem: got %q", got)
	}
	if normalizeRedeemCode("hypi-4f2k-9j3m") != "HYPI4F2K9J3M" {
		t.Fatalf("normalizeRedeemCode mismatch")
	}
	if normalizeInviteCode("AbC123") != "abc123" {
		t.Fatalf("normalizeInviteCode mismatch")
	}
	if normalizeInviteCode("bad code!") != "" {
		t.Fatalf("normalizeInviteCode should reject spaces/punct")
	}
}

func TestClaimLimiter(t *testing.T) {
	l := newClaimLimiter() // max 8 / minute
	key := "1|1.2.3.4"
	for i := 0; i < 8; i++ {
		if ok, _ := l.allow(key); !ok {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if ok, retry := l.allow(key); ok || retry <= 0 {
		t.Fatalf("9th attempt should be blocked with a retry hint, got ok=%v retry=%d", ok, retry)
	}
	// A different (user, IP) key is independent.
	if ok, _ := l.allow("2|5.6.7.8"); !ok {
		t.Fatal("a different key must not be rate-limited")
	}
}
