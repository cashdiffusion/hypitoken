package adapter

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wjsoj/cc-core/usage"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/server"
)

// chargeFixture is a funded token behind a fresh saas.db and the adapter that
// bills it.
type chargeFixture struct {
	ad   *Adapter
	info server.SaaSTokenInfo
	wsID int64
}

func newChargeFixture(t *testing.T, fundUSD float64) *chargeFixture {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "saas.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	u, err := store.CreateUser(ctx, "idem@example.test", "hash", "user", 1, true)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	tok, err := store.CreateUserToken(ctx, u.ID, db.TokenParams{Name: "idem"})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := store.AddBalance(ctx, u.ID, "topup", fundUSD, "seed", "seed", false); err != nil {
		t.Fatalf("fund: %v", err)
	}
	ad := &Adapter{DB: store}
	info, ok := ad.LookupCtx(ctx, tok.Token)
	if !ok {
		t.Fatal("token did not resolve")
	}
	ws, err := store.PersonalWorkspaceID(ctx, u.ID)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	return &chargeFixture{ad: ad, info: info, wsID: ws}
}

func (f *chargeFixture) balance(t *testing.T) float64 {
	t.Helper()
	bal, err := f.ad.DB.GetWorkspaceBalance(context.Background(), f.wsID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	return bal
}

// TestChargeWithKeyIsIdempotent is the guarantee the live path never had: the
// same charge presented twice — a retry after a lost ack — debits once, and the
// replay reports the amount the ledger already holds.
func TestChargeWithKeyIsIdempotent(t *testing.T) {
	f := newChargeFixture(t, 100)
	ctx := server.WithChargeIdemKey(context.Background(), "charge:test:main")
	counts := usage.Counts{InputTokens: 1000, OutputTokens: 100, Requests: 1}

	first, err := f.ad.Charge(ctx, f.info, "openai", "gpt-5.6-sol", counts, 10)
	if err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if first <= 0 {
		t.Fatalf("first charge moved nothing (%v)", first)
	}
	after := f.balance(t)
	if diff := (100 - first) - after; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("balance = %v after first charge, want %v", after, 100-first)
	}

	again, err := f.ad.Charge(ctx, f.info, "openai", "gpt-5.6-sol", counts, 10)
	if err != nil {
		t.Fatalf("replayed charge: %v", err)
	}
	if again != first {
		t.Fatalf("replay reported %v, want the original %v", again, first)
	}
	if got := f.balance(t); got != after {
		t.Fatalf("balance moved on replay: %v → %v", after, got)
	}
}

// TestChargeSlotsBillSeparately: keys that differ in their slot are different
// movements — the advisor sub-call must not be swallowed by the main charge's
// replay guard, nor one WS turn by the previous.
func TestChargeSlotsBillSeparately(t *testing.T) {
	f := newChargeFixture(t, 100)
	counts := usage.Counts{InputTokens: 1000, OutputTokens: 100, Requests: 1}
	main, err := f.ad.Charge(server.WithChargeIdemKey(context.Background(), "charge:req1:main"), f.info, "anthropic", "claude-sonnet-5", counts, 10)
	if err != nil {
		t.Fatalf("main: %v", err)
	}
	adv, err := f.ad.Charge(server.WithChargeIdemKey(context.Background(), "charge:req1:advisor:claude-opus-5"), f.info, "anthropic", "claude-opus-5", counts, 10)
	if err != nil {
		t.Fatalf("advisor: %v", err)
	}
	if main <= 0 || adv <= 0 {
		t.Fatalf("both slots must bill: main=%v advisor=%v", main, adv)
	}
	want := 100 - main - adv
	if got := f.balance(t); (got-want) > 1e-9 || (want-got) > 1e-9 {
		t.Fatalf("balance = %v, want %v", got, want)
	}
}

// TestChargeWithoutKeyKeepsTheOldPath: a context that names no charge — the
// CLI, an out-of-process caller that has not adopted keys — still bills, and
// bills every time, exactly as before.
func TestChargeWithoutKeyKeepsTheOldPath(t *testing.T) {
	f := newChargeFixture(t, 100)
	counts := usage.Counts{InputTokens: 1000, OutputTokens: 100, Requests: 1}
	a, err := f.ad.Charge(context.Background(), f.info, "openai", "gpt-5.6-sol", counts, 10)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := f.ad.Charge(context.Background(), f.info, "openai", "gpt-5.6-sol", counts, 10)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	want := 100 - a - b
	if got := f.balance(t); (got-want) > 1e-9 || (want-got) > 1e-9 {
		t.Fatalf("balance = %v, want %v — the keyless path must not dedupe", got, want)
	}
	if n, _, _, _ := f.ad.ChargeDrops(); n != 0 {
		t.Fatalf("no charge failed, yet %d drop(s) were recorded", n)
	}
}

// TestChargeDropIsCountedAndAlerted: a debit the ledger refuses is counted,
// surfaces its official value, and raises the alert hook exactly once inside
// the cooldown.
func TestChargeDropIsCountedAndAlerted(t *testing.T) {
	f := newChargeFixture(t, 100)
	var alerts []string
	f.ad.AlertDrop = func(detail string) { alerts = append(alerts, detail) }
	// A workspace that does not exist is a deterministic refusal, so this
	// fails fast on the first attempt rather than walking the backoff.
	bad := f.info
	bad.WorkspaceID = 999999
	counts := usage.Counts{InputTokens: 1000, OutputTokens: 100, Requests: 1}
	for i := 0; i < 3; i++ {
		if _, err := f.ad.Charge(server.WithChargeIdemKey(context.Background(), "charge:bad:main"), bad, "openai", "gpt-5.6-sol", counts, 4); err == nil {
			t.Fatal("charging a missing workspace must fail")
		}
	}
	n, official, lastErr, lastAt := f.ad.ChargeDrops()
	if n != 3 || official != 12 || lastErr == "" || lastAt.IsZero() {
		t.Fatalf("drops = (%d, $%v, %q, %v), want (3, $12, <err>, <time>)", n, official, lastErr, lastAt)
	}
	// The hook fires asynchronously; give it a moment, then check the cooldown
	// collapsed three failures into one page.
	deadline := 50
	for len(alerts) == 0 && deadline > 0 {
		deadline--
		<-timeAfter()
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts fired = %d, want exactly 1 within the cooldown", len(alerts))
	}
	if got := f.balance(t); got != 100 {
		t.Fatalf("a failed charge moved money: balance %v", got)
	}
}

func timeAfter() <-chan struct{} {
	ch := make(chan struct{})
	go func() { time.Sleep(10 * time.Millisecond); close(ch) }()
	return ch
}
