package db

import (
	"context"
	"testing"
	"time"
)

// Every user must get a personal workspace (home wallet) on creation, be its
// admin member, and have balance/User.BalanceUSD resolve to that workspace.
func TestPersonalWorkspaceAutoCreated(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "solo@example.com")

	ws, err := d.PersonalWorkspaceID(ctx, uid)
	if err != nil || ws == 0 {
		t.Fatalf("no personal workspace: id=%d err=%v", ws, err)
	}
	m, err := d.GetWorkspaceMember(ctx, ws, uid)
	if err != nil {
		t.Fatalf("owner not a member: %v", err)
	}
	if m.Role != WSRoleAdmin {
		t.Fatalf("owner role = %q, want admin", m.Role)
	}

	// A top-up credits the personal workspace, and both GetBalance and the
	// JOIN-loaded User.BalanceUSD reflect it (balance moved off users in v13).
	if _, err := d.AddBalance(ctx, uid, TxKindTopup, 7, "t", "", true); err != nil {
		t.Fatalf("topup: %v", err)
	}
	if bal, _ := d.GetBalance(ctx, uid); !approxEq(bal, 7) {
		t.Fatalf("GetBalance = %v, want 7", bal)
	}
	u, err := d.GetUser(ctx, uid)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !approxEq(u.BalanceUSD, 7) {
		t.Fatalf("User.BalanceUSD (via JOIN) = %v, want 7", u.BalanceUSD)
	}
	if u.PersonalWorkspaceID != ws {
		t.Fatalf("User.PersonalWorkspaceID = %d, want %d", u.PersonalWorkspaceID, ws)
	}
}

// A token bound to an enterprise workspace bills that shared pool, leaving the
// member's personal balance untouched; per-member and per-workspace spend
// aggregations attribute the charge correctly.
func TestEnterpriseWorkspaceBillingIsolation(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "member@corp.com")

	// Personal pool: $5.
	if _, err := d.AddBalance(ctx, uid, TxKindTopup, 5, "seed", "", true); err != nil {
		t.Fatalf("seed personal: %v", err)
	}

	// Provision an enterprise workspace with $100 and add the user as a member.
	now := time.Now().Unix()
	res, err := d.ExecContext(ctx,
		`INSERT INTO workspaces (name, type, balance_usd, created_at, updated_at) VALUES ('Corp', 'enterprise', 100, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert enterprise ws: %v", err)
	}
	entWS, _ := res.LastInsertId()
	if _, err := d.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, created_at) VALUES (?, ?, 'member', ?)`, entWS, uid, now); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// A token bound to the enterprise workspace.
	tok, err := d.CreateUserToken(ctx, uid, TokenParams{Name: "corp-key", WorkspaceID: entWS})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if tok.WorkspaceID != entWS {
		t.Fatalf("token bound to ws %d, want %d", tok.WorkspaceID, entWS)
	}

	// Charge the enterprise pool $10.
	_, charged, err := d.ChargeWorkspaceWithFloor(ctx, entWS, uid, TxKindCharge, 10, "token=1 model=x", "", 0)
	if err != nil {
		t.Fatalf("charge enterprise: %v", err)
	}
	if !approxEq(charged, 10) {
		t.Fatalf("charged = %v, want 10", charged)
	}

	// Enterprise pool debited to $90; personal pool untouched at $5.
	if entBal, _ := d.GetWorkspaceBalance(ctx, entWS); !approxEq(entBal, 90) {
		t.Fatalf("enterprise balance = %v, want 90", entBal)
	}
	if perBal, _ := d.GetBalance(ctx, uid); !approxEq(perBal, 5) {
		t.Fatalf("personal balance moved: %v, want 5", perBal)
	}

	// Spend aggregations attribute the charge to (workspace) and (workspace,member).
	since := time.Unix(0, 0)
	if s, _ := d.SumChargeSinceForWorkspace(ctx, entWS, since); !approxEq(s, 10) {
		t.Fatalf("workspace spend = %v, want 10", s)
	}
	if s, _ := d.SumChargeSinceForMember(ctx, entWS, uid, since); !approxEq(s, 10) {
		t.Fatalf("member spend = %v, want 10", s)
	}
}

// effectiveMonthlyCap-style folding lives in the adapter; here we just confirm
// the per-member cap query is scoped so a charge in one workspace does not count
// against a member's cap usage in another.
func TestMemberSpendScopedToWorkspace(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	uid := mkUser(t, d, "scoped@corp.com")
	personalWS, _ := d.PersonalWorkspaceID(ctx, uid)

	// Charge the personal workspace; it must NOT show up in an enterprise
	// workspace's member-spend aggregation.
	if _, err := d.AddBalance(ctx, uid, TxKindTopup, 20, "seed", "", true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := d.ChargeWorkspaceWithFloor(ctx, personalWS, uid, TxKindCharge, 3, "token=1 model=x", "", 0); err != nil {
		t.Fatalf("charge personal: %v", err)
	}

	now := time.Now().Unix()
	res, _ := d.ExecContext(ctx, `INSERT INTO workspaces (name, type, balance_usd, created_at, updated_at) VALUES ('Corp', 'enterprise', 50, ?, ?)`, now, now)
	entWS, _ := res.LastInsertId()
	since := time.Unix(0, 0)
	if s, _ := d.SumChargeSinceForMember(ctx, entWS, uid, since); !approxEq(s, 0) {
		t.Fatalf("member spend leaked across workspaces: %v, want 0", s)
	}
	if s, _ := d.SumChargeSinceForMember(ctx, personalWS, uid, since); !approxEq(s, 3) {
		t.Fatalf("personal member spend = %v, want 3", s)
	}
}
