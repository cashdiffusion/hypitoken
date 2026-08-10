package support

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

func newSvc(t *testing.T) *Service {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return New(store, "Test", "http://localhost:8317")
}

// TestAppealAccessKeyIsTheOnlyHandle covers the capability model for an appeal
// filed without a session: the key returned once at submission reads the thread,
// and nothing else does. Getting this wrong either locks out a wrongly-banned
// user or exposes every ticket to anyone who can count.
func TestAppealAccessKeyIsTheOnlyHandle(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)

	tk, err := svc.Create(ctx, 0, "banned@example.com", KindAppeal, "", "我的账号被封了，我是正常用户")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if tk.AccessKey == "" {
		t.Fatal("anonymous appeal must return an access key")
	}
	if tk.Status != StatusOpen || tk.Kind != KindAppeal {
		t.Fatalf("want open/appeal, got %s/%s", tk.Status, tk.Kind)
	}

	if _, err := svc.GetForKey(ctx, tk.ID, tk.AccessKey); err != nil {
		t.Fatalf("read with correct key: %v", err)
	}
	if _, err := svc.GetForKey(ctx, tk.ID, "wrong"); err == nil {
		t.Fatal("wrong key must not read the ticket")
	}
	// The empty key is the dangerous case: a signed-in user's ticket stores "",
	// so an empty-matches-empty bug would expose every one of them.
	if _, err := svc.GetForKey(ctx, tk.ID, ""); err == nil {
		t.Fatal("empty key must not read the ticket")
	}
	owned, err := svc.Create(ctx, 42, "user@example.com", KindSupport, "问题", "怎么用")
	if err != nil {
		t.Fatalf("create owned: %v", err)
	}
	if _, err := svc.GetForKey(ctx, owned.ID, ""); err == nil {
		t.Fatal("empty key must not read a session-owned ticket")
	}
}

// TestReplyStatusFlow covers who-spoke-last: an operator reply parks the ticket
// awaiting the user, a user reply reopens it — including from a terminal state,
// which is how someone contests a rejected appeal.
func TestReplyStatusFlow(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)

	tk, err := svc.Create(ctx, 7, "user@example.com", KindSupport, "标题", "第一条")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Reply(ctx, tk.ID, AuthorAdmin, "已收到，正在处理"); err != nil {
		t.Fatalf("admin reply: %v", err)
	}
	got, _ := svc.Get(ctx, tk.ID)
	if got.Status != StatusPending || got.LastActor != AuthorAdmin {
		t.Fatalf("after admin reply: want pending/admin, got %s/%s", got.Status, got.LastActor)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("want 2 messages, got %d", len(got.Messages))
	}

	if err := svc.SetStatus(ctx, tk.ID, StatusRejected); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if err := svc.Reply(ctx, tk.ID, AuthorUser, "我不同意这个结论"); err != nil {
		t.Fatalf("user reply after rejection: %v", err)
	}
	got, _ = svc.Get(ctx, tk.ID)
	if got.Status != StatusOpen {
		t.Fatalf("a contested rejection must reopen: got %s", got.Status)
	}

	if err := svc.Reply(ctx, tk.ID, AuthorUser, "   "); err == nil {
		t.Fatal("empty reply must be rejected")
	}
	if err := svc.SetStatus(ctx, tk.ID, "nonsense"); err == nil {
		t.Fatal("invalid status must be rejected")
	}
}

// TestAdminQueueOrdering covers the operator queue: live tickets first, and the
// status filter the panel's tabs drive.
func TestAdminQueueOrdering(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)

	resolved, _ := svc.Create(ctx, 1, "a@example.com", KindSupport, "旧的", "x")
	if err := svc.SetStatus(ctx, resolved.ID, StatusResolved); err != nil {
		t.Fatalf("set status: %v", err)
	}
	open1, _ := svc.Create(ctx, 2, "b@example.com", KindAppeal, "新的", "y")

	items, total, err := svc.ListForAdmin(ctx, "", 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Fatalf("total: want 2, got %d", total)
	}
	if items[0].ID != open1.ID {
		t.Fatalf("live tickets must sort first, got id=%d", items[0].ID)
	}
	if _, total, _ = svc.ListForAdmin(ctx, StatusResolved, 50, 0); total != 1 {
		t.Fatalf("resolved filter: want 1, got %d", total)
	}
	if _, total, _ = svc.ListForAdmin(ctx, "live", 50, 0); total != 1 {
		t.Fatalf("live filter: want 1, got %d", total)
	}
	if n := svc.OpenCount(ctx); n != 1 {
		t.Fatalf("open count: want 1, got %d", n)
	}
	// A user only ever sees their own.
	if _, total, _ := svc.ListForUser(ctx, 2, 50, 0); total != 1 {
		t.Fatalf("user list: want 1, got %d", total)
	}
	if _, total, _ := svc.ListForUser(ctx, 999, 50, 0); total != 0 {
		t.Fatalf("stranger list: want 0, got %d", total)
	}
}

// TestRateLimiterWindows pins the appeal limiter. With no OTP in front of the
// endpoint this is the only guard it has, so both axes matter: 3/hour per
// address, 20/day per IP for an attacker rotating addresses.
func TestRateLimiterWindows(t *testing.T) {
	l := newRateLimiter()
	for i := range 3 {
		if ok, _ := l.allowSubmit("a@example.com", "1.2.3.4"); !ok {
			t.Fatalf("submit %d must pass", i)
		}
	}
	ok, retry := l.allowSubmit("a@example.com", "1.2.3.4")
	if ok {
		t.Fatal("fourth submit within the hour must be refused")
	}
	if retry <= 0 || retry > 3600 {
		t.Fatalf("retry-after out of range: %d", retry)
	}
	// A different address from a different IP is unaffected.
	if ok, _ := l.allowSubmit("b@example.com", "5.6.7.8"); !ok {
		t.Fatal("unrelated submitter must pass")
	}
	// Rotating the address does not escape the per-IP daily ceiling.
	blocked := false
	for i := range 30 {
		if ok, _ := l.allowSubmit(fmt.Sprintf("burner%d@example.com", i), "9.9.9.9"); !ok {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("address rotation must still hit the per-IP daily cap")
	}
}
