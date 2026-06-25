package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

const (
	InvitePending  = "pending"
	InviteAccepted = "accepted"
	InviteRevoked  = "revoked"
	InviteExpired  = "expired"
)

type WorkspaceInvite struct {
	ID             int64
	WorkspaceID    int64
	Email          string
	Role           string
	Token          string
	Status         string
	InvitedBy      int64
	AcceptedUserID int64
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

// EffectiveStatus folds in expiry: a pending invite past its deadline reads as
// expired without needing a background sweeper.
func (i *WorkspaceInvite) EffectiveStatus() string {
	if i.Status == InvitePending && !i.ExpiresAt.IsZero() && time.Now().After(i.ExpiresAt) {
		return InviteExpired
	}
	return i.Status
}

func genInviteToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

const inviteCols = `id, workspace_id, email, role, token, status, invited_by, accepted_user_id, expires_at, created_at`

func scanInvite(row interface{ Scan(...any) error }) (*WorkspaceInvite, error) {
	var i WorkspaceInvite
	var exp, created int64
	if err := row.Scan(&i.ID, &i.WorkspaceID, &i.Email, &i.Role, &i.Token, &i.Status, &i.InvitedBy, &i.AcceptedUserID, &exp, &created); err != nil {
		return nil, err
	}
	if exp > 0 {
		i.ExpiresAt = time.Unix(exp, 0)
	}
	i.CreatedAt = time.Unix(created, 0)
	return &i, nil
}

// CreateWorkspaceInvite issues a pending invite for an email. If a pending
// invite for the same (workspace, email) already exists it is refreshed (new
// token + expiry) rather than duplicated.
func (db *DB) CreateWorkspaceInvite(ctx context.Context, workspaceID int64, email, role string, invitedBy int64, ttl time.Duration) (*WorkspaceInvite, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, errors.New("email required")
	}
	if role != WSRoleAdmin && role != WSRoleMember {
		role = WSRoleMember
	}
	tok, err := genInviteToken()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	exp := now.Add(ttl).Unix()

	// Refresh an existing pending invite in place.
	var existingID int64
	_ = db.QueryRowContext(ctx,
		`SELECT id FROM workspace_invites WHERE workspace_id = ? AND email = ? AND status = ?`,
		workspaceID, email, InvitePending).Scan(&existingID)
	if existingID > 0 {
		if _, err := db.ExecContext(ctx,
			`UPDATE workspace_invites SET role = ?, token = ?, expires_at = ?, invited_by = ? WHERE id = ?`,
			role, tok, exp, invitedBy, existingID); err != nil {
			return nil, err
		}
		return db.getInvite(ctx, existingID)
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO workspace_invites (workspace_id, email, role, token, status, invited_by, accepted_user_id, expires_at, created_at)
		 VALUES (?, ?, ?, ?, 'pending', ?, 0, ?, ?)`,
		workspaceID, email, role, tok, invitedBy, exp, now.Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.getInvite(ctx, id)
}

func (db *DB) getInvite(ctx context.Context, id int64) (*WorkspaceInvite, error) {
	row := db.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM workspace_invites WHERE id = ?`, id)
	i, err := scanInvite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return i, err
}

// GetWorkspaceInviteByToken loads an invite by its link token.
func (db *DB) GetWorkspaceInviteByToken(ctx context.Context, token string) (*WorkspaceInvite, error) {
	row := db.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM workspace_invites WHERE token = ?`, token)
	i, err := scanInvite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return i, err
}

// ListWorkspaceInvites returns all invites for a workspace, newest first.
func (db *DB) ListWorkspaceInvites(ctx context.Context, workspaceID int64) ([]*WorkspaceInvite, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+inviteCols+` FROM workspace_invites WHERE workspace_id = ? ORDER BY id DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WorkspaceInvite
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// ListPendingInvitesForEmail returns all still-valid pending invites addressed
// to an email — used at registration to auto-claim them.
func (db *DB) ListPendingInvitesForEmail(ctx context.Context, email string) ([]*WorkspaceInvite, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	rows, err := db.QueryContext(ctx,
		`SELECT `+inviteCols+` FROM workspace_invites WHERE email = ? AND status = ?`, email, InvitePending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WorkspaceInvite
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		if i.EffectiveStatus() == InvitePending {
			out = append(out, i)
		}
	}
	return out, rows.Err()
}

// RevokeWorkspaceInvite marks a pending invite revoked.
func (db *DB) RevokeWorkspaceInvite(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `UPDATE workspace_invites SET status = ? WHERE id = ? AND status = ?`, InviteRevoked, id, InvitePending)
	return err
}

// RefreshWorkspaceInvite re-issues a pending invite's token + expiry (resend).
func (db *DB) RefreshWorkspaceInvite(ctx context.Context, id int64, ttl time.Duration) (*WorkspaceInvite, error) {
	tok, err := genInviteToken()
	if err != nil {
		return nil, err
	}
	exp := time.Now().Add(ttl).Unix()
	if _, err := db.ExecContext(ctx,
		`UPDATE workspace_invites SET token = ?, expires_at = ?, status = 'pending' WHERE id = ?`, tok, exp, id); err != nil {
		return nil, err
	}
	return db.getInvite(ctx, id)
}

// AcceptWorkspaceInvite consumes a pending invite for userID: it creates the
// membership and marks the invite accepted, atomically. The caller must have
// verified the invite's email matches the user. Returns the invite.
func (db *DB) AcceptWorkspaceInvite(ctx context.Context, inv *WorkspaceInvite, userID int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	role := inv.Role
	if role != WSRoleAdmin && role != WSRoleMember {
		role = WSRoleMember
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(workspace_id, user_id) DO UPDATE SET role = excluded.role`,
		inv.WorkspaceID, userID, role, time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE workspace_invites SET status = 'accepted', accepted_user_id = ? WHERE id = ?`, userID, inv.ID); err != nil {
		return err
	}
	return tx.Commit()
}
