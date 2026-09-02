package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Workspace is the billing / quota subject. Every API token bills exactly one
// workspace's balance pool. A `personal` workspace is auto-created 1:1 for each
// user (their old individual wallet); `enterprise` workspaces are provisioned by
// the platform admin and shared across invited members.
type Workspace struct {
	ID            int64
	Name          string
	Type          string // personal | enterprise
	BalanceUSD    float64
	DailyUSDCap   float64
	MonthlyUSDCap float64
	GroupID       int64 // legacy pricing group (frozen; no longer drives billing)
	// Per-workspace billing multipliers. 0 = use the standard default
	// (0.3 claude / 0.05 codex). Only enterprise workspaces set custom rates.
	ClaudeMultiplier float64
	CodexMultiplier  float64
	CreatedBy        int64
	Disabled         bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// WorkspaceMember is a user's membership + role in a workspace.
type WorkspaceMember struct {
	ID            int64
	WorkspaceID   int64
	UserID        int64
	Role          string // admin | member
	MonthlyUSDCap float64
	CreatedAt     time.Time
}

const (
	WorkspaceTypePersonal   = "personal"
	WorkspaceTypeEnterprise = "enterprise"

	WSRoleAdmin  = "admin"
	WSRoleMember = "member"
)

const workspaceCols = `id, name, type, balance_usd, daily_usd_cap, monthly_usd_cap, group_id, claude_multiplier, codex_multiplier, created_by, disabled, created_at, updated_at`

func scanWorkspace(row interface{ Scan(...any) error }) (*Workspace, error) {
	var w Workspace
	var dis int
	var createdAt, updatedAt int64
	if err := row.Scan(&w.ID, &w.Name, &w.Type, &w.BalanceUSD, &w.DailyUSDCap, &w.MonthlyUSDCap,
		&w.GroupID, &w.ClaudeMultiplier, &w.CodexMultiplier, &w.CreatedBy, &dis, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	w.Disabled = dis != 0
	w.CreatedAt = time.Unix(createdAt, 0)
	w.UpdatedAt = time.Unix(updatedAt, 0)
	return &w, nil
}

// GetWorkspace loads a workspace by id.
func (db *DB) GetWorkspace(ctx context.Context, id int64) (*Workspace, error) {
	row := db.QueryRowContext(ctx, `SELECT `+workspaceCols+` FROM workspaces WHERE id = ?`, id)
	w, err := scanWorkspace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return w, err
}

// CreatePersonalWorkspace creates the 1:1 personal workspace for a freshly
// registered user, points users.personal_workspace_id at it, and records the
// user as its admin — all atomically. Returns the new workspace id. Idempotent
// guard: if the user already has a personal workspace it is returned as-is.
func (db *DB) CreatePersonalWorkspace(ctx context.Context, userID int64, email string, groupID int64) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var existing int64
	_ = tx.QueryRowContext(ctx, `SELECT personal_workspace_id FROM users WHERE id = ?`, userID).Scan(&existing)
	if existing > 0 {
		return existing, nil
	}

	now := time.Now().Unix()
	name := strings.TrimSpace(email)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO workspaces (name, type, balance_usd, group_id, created_by, created_at, updated_at)
		 VALUES (?, 'personal', 0, ?, ?, ?, ?)`,
		name, groupID, userID, now, now)
	if err != nil {
		return 0, err
	}
	wsID, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET personal_workspace_id = ?, updated_at = ? WHERE id = ?`, wsID, now, userID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, created_at) VALUES (?, ?, 'admin', ?)`,
		wsID, userID, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return wsID, nil
}

// CreateEnterpriseWorkspace provisions a shared enterprise workspace (platform
// admin only). createdBy is the operator's user id. claudeMult/codexMult are the
// per-workspace billing multipliers (0 = standard default).
//
// It always opens at $0. It used to take an opening balance and write it
// straight into the row — the one place in the codebase that moved money
// without a wallet_tx line, and the origin of a $50 workspace with no ledger
// behind it. Funding goes through AdjustWorkspaceBalance so the audit trail is
// complete from the first cent.
func (db *DB) CreateEnterpriseWorkspace(ctx context.Context, name string, dailyCap, monthlyCap, claudeMult, codexMult float64, createdBy int64) (*Workspace, error) {
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx,
		`INSERT INTO workspaces (name, type, balance_usd, daily_usd_cap, monthly_usd_cap, claude_multiplier, codex_multiplier, created_by, created_at, updated_at)
		 VALUES (?, 'enterprise', 0, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(name), dailyCap, monthlyCap, claudeMult, codexMult, createdBy, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.GetWorkspace(ctx, id)
}

// WorkspaceWithMeta is a workspace plus a member count, for admin listings.
type WorkspaceWithMeta struct {
	Workspace
	MemberCount int
}

// ListWorkspaces returns workspaces (optionally filtered by type) with member
// counts, plus the total count, newest first.
func (db *DB) ListWorkspaces(ctx context.Context, typ string, limit, offset int) ([]*WorkspaceWithMeta, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	where := ""
	args := []any{}
	if typ != "" {
		where = ` WHERE type = ?`
		args = append(args, typ)
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx,
		`SELECT `+workspaceCols+`, (SELECT COUNT(*) FROM workspace_members m WHERE m.workspace_id = workspaces.id)
		   FROM workspaces`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*WorkspaceWithMeta
	for rows.Next() {
		var w Workspace
		var dis int
		var createdAt, updatedAt int64
		var memberCount int
		if err := rows.Scan(&w.ID, &w.Name, &w.Type, &w.BalanceUSD, &w.DailyUSDCap, &w.MonthlyUSDCap,
			&w.GroupID, &w.ClaudeMultiplier, &w.CodexMultiplier, &w.CreatedBy, &dis, &createdAt, &updatedAt, &memberCount); err != nil {
			return nil, 0, err
		}
		w.Disabled = dis != 0
		w.CreatedAt = time.Unix(createdAt, 0)
		w.UpdatedAt = time.Unix(updatedAt, 0)
		out = append(out, &WorkspaceWithMeta{Workspace: w, MemberCount: memberCount})
	}
	return out, total, rows.Err()
}

// WorkspaceUpdate carries optional workspace field changes from the admin panel.
type WorkspaceUpdate struct {
	Name             *string
	DailyUSDCap      *float64
	MonthlyUSDCap    *float64
	ClaudeMultiplier *float64
	CodexMultiplier  *float64
	Disabled         *bool
}

// UpdateWorkspace applies the non-nil fields of u to a workspace.
func (db *DB) UpdateWorkspace(ctx context.Context, id int64, u WorkspaceUpdate) error {
	sets := []string{}
	args := []any{}
	if u.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, strings.TrimSpace(*u.Name))
	}
	if u.DailyUSDCap != nil {
		sets = append(sets, "daily_usd_cap = ?")
		args = append(args, *u.DailyUSDCap)
	}
	if u.MonthlyUSDCap != nil {
		sets = append(sets, "monthly_usd_cap = ?")
		args = append(args, *u.MonthlyUSDCap)
	}
	if u.ClaudeMultiplier != nil {
		sets = append(sets, "claude_multiplier = ?")
		args = append(args, *u.ClaudeMultiplier)
	}
	if u.CodexMultiplier != nil {
		sets = append(sets, "codex_multiplier = ?")
		args = append(args, *u.CodexMultiplier)
	}
	if u.Disabled != nil {
		d := 0
		if *u.Disabled {
			d = 1
		}
		sets = append(sets, "disabled = ?")
		args = append(args, d)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().Unix(), id)
	_, err := db.ExecContext(ctx, `UPDATE workspaces SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}

// AdjustWorkspaceBalance applies a signed delta to a workspace's pool, recording
// a wallet_tx adjustment attributed to the operator. allowNegative is forced on
// (admin override). Returns the new balance.
func (db *DB) AdjustWorkspaceBalance(ctx context.Context, workspaceID, opUserID int64, deltaUSD float64, ref, note string) (float64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	bal, err := addWorkspaceBalanceTx(ctx, tx, workspaceID, opUserID, TxKindAdjust, deltaUSD, ref, note, true)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return bal, nil
}

// UpsertWorkspaceMember adds a member to a workspace or updates their role.
func (db *DB) UpsertWorkspaceMember(ctx context.Context, workspaceID, userID int64, role string) error {
	if role != WSRoleAdmin && role != WSRoleMember {
		role = WSRoleMember
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(workspace_id, user_id) DO UPDATE SET role = excluded.role`,
		workspaceID, userID, role, time.Now().Unix())
	return err
}

// RemoveWorkspaceMember removes a user from a workspace.
func (db *DB) RemoveWorkspaceMember(ctx context.Context, workspaceID, userID int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, workspaceID, userID)
	return err
}

// SetMemberMonthlyCap sets a member's per-member monthly cap within a workspace.
func (db *DB) SetMemberMonthlyCap(ctx context.Context, workspaceID, userID int64, monthlyCap float64) error {
	_, err := db.ExecContext(ctx, `UPDATE workspace_members SET monthly_usd_cap = ? WHERE workspace_id = ? AND user_id = ?`, monthlyCap, workspaceID, userID)
	return err
}

// WorkspaceMemberInfo is a member row joined to the user's email + their spend.
type WorkspaceMemberInfo struct {
	UserID        int64
	Email         string
	Role          string
	MonthlyUSDCap float64
	CreatedAt     time.Time
}

// ListWorkspaceMembers returns a workspace's members joined with their email.
func (db *DB) ListWorkspaceMembers(ctx context.Context, workspaceID int64) ([]*WorkspaceMemberInfo, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT m.user_id, COALESCE(u.email,''), m.role, m.monthly_usd_cap, m.created_at
		   FROM workspace_members m LEFT JOIN users u ON u.id = m.user_id
		  WHERE m.workspace_id = ? ORDER BY m.role, u.email`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WorkspaceMemberInfo
	for rows.Next() {
		var m WorkspaceMemberInfo
		var createdAt int64
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role, &m.MonthlyUSDCap, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, &m)
	}
	return out, rows.Err()
}

// MemberWorkspace is an enterprise workspace a user belongs to, with their role
// and the workspace's effective billing multipliers (0 = standard default).
type MemberWorkspace struct {
	WorkspaceID      int64
	Name             string
	Type             string
	Role             string
	ClaudeMultiplier float64
	CodexMultiplier  float64
}

// ListWorkspacesForUser returns the workspaces a user is a member of (with their
// role + billing rate), personal first. Powers /api/v2/me, the token
// billing-target picker, and the dashboard pricing card.
func (db *DB) ListWorkspacesForUser(ctx context.Context, userID int64) ([]*MemberWorkspace, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT w.id, w.name, w.type, m.role, w.claude_multiplier, w.codex_multiplier
		   FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
		  WHERE m.user_id = ? AND w.disabled = 0
		  ORDER BY CASE w.type WHEN 'personal' THEN 0 ELSE 1 END, w.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MemberWorkspace
	for rows.Next() {
		var m MemberWorkspace
		if err := rows.Scan(&m.WorkspaceID, &m.Name, &m.Type, &m.Role, &m.ClaudeMultiplier, &m.CodexMultiplier); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// MemberUsage is a workspace member with their spend in a window — the
// credential-free per-member view a space admin sees.
type MemberUsage struct {
	UserID        int64
	Email         string
	Role          string
	MonthlyUSDCap float64
	SpentUSD      float64
}

// WorkspaceMemberUsage lists members of a workspace with their charge total
// since `since`. No upstream-credential info is exposed (audit-safe for space
// admins).
func (db *DB) WorkspaceMemberUsage(ctx context.Context, workspaceID int64, since time.Time) ([]*MemberUsage, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT m.user_id, COALESCE(u.email,''), m.role, m.monthly_usd_cap,
		        COALESCE((SELECT SUM(-w.amount_usd) FROM wallet_tx w
		                   WHERE w.workspace_id = m.workspace_id AND w.user_id = m.user_id
		                     AND w.kind = 'charge' AND w.created_at >= ?), 0) AS spent
		   FROM workspace_members m LEFT JOIN users u ON u.id = m.user_id
		  WHERE m.workspace_id = ?
		  ORDER BY spent DESC`, since.Unix(), workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MemberUsage
	for rows.Next() {
		var m MemberUsage
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role, &m.MonthlyUSDCap, &m.SpentUSD); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// WorkspaceLedgerRow is one charge line in a workspace's audit ledger. It
// carries the member, key ref, model, billed amount and time — but NO upstream
// credential identity (wallet_tx has none), satisfying the visibility boundary.
type WorkspaceLedgerRow struct {
	UserID    int64
	Email     string
	Ref       string
	AmountUSD float64
	CreatedAt time.Time
}

// WorkspaceLedger returns a page of a workspace's charge ledger (newest first)
// plus the total count.
func (db *DB) WorkspaceLedger(ctx context.Context, workspaceID int64, limit, offset int) ([]*WorkspaceLedgerRow, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wallet_tx WHERE workspace_id = ? AND kind = 'charge'`, workspaceID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT w.user_id, COALESCE(u.email,''), w.ref, w.amount_usd, w.created_at
		   FROM wallet_tx w LEFT JOIN users u ON u.id = w.user_id
		  WHERE w.workspace_id = ? AND w.kind = 'charge'
		  ORDER BY w.id DESC LIMIT ? OFFSET ?`, workspaceID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*WorkspaceLedgerRow
	for rows.Next() {
		var r WorkspaceLedgerRow
		var created int64
		if err := rows.Scan(&r.UserID, &r.Email, &r.Ref, &r.AmountUSD, &created); err != nil {
			return nil, 0, err
		}
		r.CreatedAt = time.Unix(created, 0)
		out = append(out, &r)
	}
	return out, total, rows.Err()
}

// GetWorkspaceMember returns the membership row for (workspace, user), or
// ErrNotFound if the user is not a member.
func (db *DB) GetWorkspaceMember(ctx context.Context, workspaceID, userID int64) (*WorkspaceMember, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, workspace_id, user_id, role, monthly_usd_cap, created_at
		   FROM workspace_members WHERE workspace_id = ? AND user_id = ?`, workspaceID, userID)
	var m WorkspaceMember
	var createdAt int64
	if err := row.Scan(&m.ID, &m.WorkspaceID, &m.UserID, &m.Role, &m.MonthlyUSDCap, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.CreatedAt = time.Unix(createdAt, 0)
	return &m, nil
}

// personalWorkspaceIDTx resolves a user's personal (home) workspace id within a
// transaction. Reads the direct pointer first; falls back to a created_by lookup
// for defensive robustness (should never be needed post-migration).
func personalWorkspaceIDTx(ctx context.Context, tx *sql.Tx, userID int64) (int64, error) {
	var ws int64
	if err := tx.QueryRowContext(ctx, `SELECT personal_workspace_id FROM users WHERE id = ?`, userID).Scan(&ws); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if ws == 0 {
		_ = tx.QueryRowContext(ctx,
			`SELECT id FROM workspaces WHERE created_by = ? AND type = 'personal' ORDER BY id LIMIT 1`, userID).Scan(&ws)
	}
	if ws == 0 {
		return 0, ErrNotFound
	}
	return ws, nil
}
