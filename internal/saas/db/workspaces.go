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
	GroupID       int64 // pricing group; 0 = default
	CreatedBy     int64
	Disabled      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
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

const workspaceCols = `id, name, type, balance_usd, daily_usd_cap, monthly_usd_cap, group_id, created_by, disabled, created_at, updated_at`

func scanWorkspace(row interface{ Scan(...any) error }) (*Workspace, error) {
	var w Workspace
	var dis int
	var createdAt, updatedAt int64
	if err := row.Scan(&w.ID, &w.Name, &w.Type, &w.BalanceUSD, &w.DailyUSDCap, &w.MonthlyUSDCap,
		&w.GroupID, &w.CreatedBy, &dis, &createdAt, &updatedAt); err != nil {
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
