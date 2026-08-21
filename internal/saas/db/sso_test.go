package db

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func mkSSOUser(t *testing.T, d *DB, email string) int64 {
	t.Helper()
	return mkUser(t, d, email)
}

// TestCreateSSOCodeStoresOnlyTheHash is the property migration v23's comment
// is about: a dump of sso_codes must not contain anything redeemable.
func TestCreateSSOCodeStoresOnlyTheHash(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkSSOUser(t, d, "sso-hash@example.com")

	raw, err := d.CreateSSOCode(ctx, uid, "https://hub.novadiffusion.com/sso", SSOCodeTTL)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 32 bytes of entropy, RawURLEncoding, no padding.
	if len(raw) != 43 {
		t.Fatalf("code length = %d, want 43 (32 bytes base64url unpadded)", len(raw))
	}
	if strings.ContainsAny(raw, "+/=") {
		t.Fatalf("code %q is not URL-safe", raw)
	}

	var stored string
	if err := d.QueryRowContext(ctx, `SELECT code FROM sso_codes WHERE user_id = ?`, uid).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored == raw {
		t.Fatal("raw code was stored in the clear — a DB leak would hand out live sessions")
	}
	if stored != hashSSOCode(raw) {
		t.Fatalf("stored value is not sha256(raw): %q", stored)
	}
	if len(stored) != 64 {
		t.Fatalf("stored hash length = %d, want 64 hex chars", len(stored))
	}

	// Two codes for the same user must differ.
	other, err := d.CreateSSOCode(ctx, uid, "https://hub.novadiffusion.com/sso", SSOCodeTTL)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if other == raw {
		t.Fatal("two mints produced the same code")
	}
}

// TestRedeemSSOCodeHappyPath — the whole point: one redeem returns the minting
// user and the destination the code was minted for.
func TestRedeemSSOCodeHappyPath(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkSSOUser(t, d, "sso-ok@example.com")
	const want = "https://hub.novadiffusion.com/sso"

	raw, err := d.CreateSSOCode(ctx, uid, want, SSOCodeTTL)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gotUID, gotURL, err := d.RedeemSSOCode(ctx, raw)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if gotUID != uid {
		t.Fatalf("user id = %d, want %d", gotUID, uid)
	}
	if gotURL != want {
		t.Fatalf("return url = %q, want %q", gotURL, want)
	}

	// The row is marked spent, not deleted.
	var usedAt int64
	if err := d.QueryRowContext(ctx, `SELECT used_at FROM sso_codes WHERE code = ?`, hashSSOCode(raw)).Scan(&usedAt); err != nil {
		t.Fatalf("read used_at: %v", err)
	}
	if usedAt == 0 {
		t.Fatal("used_at still 0 after a successful redeem")
	}
}

// TestRedeemSSOCodeIsOneShot — a replay must fail. This is the difference
// between a handoff code and a password.
func TestRedeemSSOCodeIsOneShot(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkSSOUser(t, d, "sso-replay@example.com")

	raw, err := d.CreateSSOCode(ctx, uid, "https://hub.novadiffusion.com/sso", SSOCodeTTL)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := d.RedeemSSOCode(ctx, raw); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	_, _, err = d.RedeemSSOCode(ctx, raw)
	if !errors.Is(err, ErrSSOCodeInvalid) {
		t.Fatalf("second redeem err = %v, want ErrSSOCodeInvalid", err)
	}
}

// TestRedeemSSOCodeRejections checks that expired, unknown, blank, and spent
// codes are INDISTINGUISHABLE — one sentinel, so the endpoint above cannot
// become an oracle for which codes exist.
func TestRedeemSSOCodeRejections(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkSSOUser(t, d, "sso-reject@example.com")

	expired, err := d.CreateSSOCode(ctx, uid, "https://hub.novadiffusion.com/sso", -time.Second)
	if err != nil {
		t.Fatalf("create expired: %v", err)
	}
	// A negative TTL falls back to the default, so age the row explicitly.
	if _, err := d.ExecContext(ctx, `UPDATE sso_codes SET expires_at = ? WHERE code = ?`,
		time.Now().Add(-time.Minute).Unix(), hashSSOCode(expired)); err != nil {
		t.Fatalf("age row: %v", err)
	}

	spent, err := d.CreateSSOCode(ctx, uid, "https://hub.novadiffusion.com/sso", SSOCodeTTL)
	if err != nil {
		t.Fatalf("create spent: %v", err)
	}
	if _, _, err := d.RedeemSSOCode(ctx, spent); err != nil {
		t.Fatalf("spend: %v", err)
	}

	for _, tc := range []struct{ name, code string }{
		{"expired", expired},
		{"already used", spent},
		{"unknown", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{"empty", ""},
		{"whitespace", "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uid, url, err := d.RedeemSSOCode(ctx, tc.code)
			if !errors.Is(err, ErrSSOCodeInvalid) {
				t.Fatalf("err = %v, want ErrSSOCodeInvalid", err)
			}
			if uid != 0 || url != "" {
				t.Fatalf("leaked (%d, %q) on a failed redeem", uid, url)
			}
		})
	}
}

// TestRedeemSSOCodeConcurrent fires 16 simultaneous redeems of ONE code.
// Exactly one may win. This is the test that would fail if the guard were the
// SELECT rather than the UPDATE's RowsAffected.
func TestRedeemSSOCodeConcurrent(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkSSOUser(t, d, "sso-race@example.com")

	raw, err := d.CreateSSOCode(ctx, uid, "https://hub.novadiffusion.com/sso", SSOCodeTTL)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	const n = 16
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		wins    int
		unknown []error
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			gotUID, _, err := d.RedeemSSOCode(ctx, raw)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
				if gotUID != uid {
					unknown = append(unknown, errors.New("winner got the wrong user id"))
				}
			case errors.Is(err, ErrSSOCodeInvalid):
				// expected loser
			default:
				unknown = append(unknown, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(unknown) > 0 {
		t.Fatalf("unexpected errors: %v", unknown)
	}
	if wins != 1 {
		t.Fatalf("%d of %d concurrent redeems succeeded, want exactly 1", wins, n)
	}
}

// TestPruneSSOCodes only collects rows past the cutoff — a live code must
// survive the sweep that runs while it is in flight.
func TestPruneSSOCodes(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkSSOUser(t, d, "sso-prune@example.com")

	live, err := d.CreateSSOCode(ctx, uid, "https://hub.novadiffusion.com/sso", SSOCodeTTL)
	if err != nil {
		t.Fatalf("create live: %v", err)
	}
	old, err := d.CreateSSOCode(ctx, uid, "https://hub.novadiffusion.com/sso", SSOCodeTTL)
	if err != nil {
		t.Fatalf("create old: %v", err)
	}
	if _, err := d.ExecContext(ctx, `UPDATE sso_codes SET expires_at = ? WHERE code = ?`,
		time.Now().Add(-2*time.Hour).Unix(), hashSSOCode(old)); err != nil {
		t.Fatalf("age row: %v", err)
	}

	n, err := d.PruneSSOCodes(ctx, time.Now().Add(-time.Hour).Unix())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned %d rows, want 1", n)
	}
	if _, _, err := d.RedeemSSOCode(ctx, live); err != nil {
		t.Fatalf("live code did not survive the prune: %v", err)
	}
	if _, _, err := d.RedeemSSOCode(ctx, old); !errors.Is(err, ErrSSOCodeInvalid) {
		t.Fatalf("pruned code err = %v, want ErrSSOCodeInvalid", err)
	}
}

// TestCreateSSOCodeRejectsBadInput — a code minted for user 0, or for nowhere,
// is a bug, not a request to satisfy.
func TestCreateSSOCodeRejectsBadInput(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	if _, err := d.CreateSSOCode(ctx, 0, "https://hub.novadiffusion.com/sso", SSOCodeTTL); err == nil {
		t.Fatal("minted a code for user id 0")
	}
	uid := mkSSOUser(t, d, "sso-bad@example.com")
	if _, err := d.CreateSSOCode(ctx, uid, "   ", SSOCodeTTL); err == nil {
		t.Fatal("minted a code with a blank return url")
	}
}
