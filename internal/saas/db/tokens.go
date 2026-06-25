package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"math/big"
	"time"
)

type UserToken struct {
	ID            int64
	UserID        int64
	Token         string
	Name          string
	DailyUSDCap   float64
	MonthlyUSDCap float64
	MaxConcurrent int
	RPM           int
	Disabled      bool
	LastUsedAt    time.Time
	CreatedAt     time.Time
	// Groups is the priority-ordered credential-group fallthrough list.
	// Empty = inherit user.GroupID (legacy single-group routing).
	Groups []string
	// WorkspaceID is the billing subject this token charges (personal or
	// enterprise). AdminMonthlyCap is a space-admin-imposed per-key cap (0 =
	// none); the effective cap is min(MonthlyUSDCap, AdminMonthlyCap>0).
	WorkspaceID     int64
	AdminMonthlyCap float64
}

const tokenCols = `id, user_id, token, name, daily_usd_cap, monthly_usd_cap, max_concurrent, rpm, disabled, last_used_at, created_at, groups, workspace_id, admin_monthly_cap`

func scanToken(row interface{ Scan(...any) error }) (*UserToken, error) {
	var t UserToken
	var disabled int
	var lastUsed, created int64
	var groupsJSON string
	if err := row.Scan(&t.ID, &t.UserID, &t.Token, &t.Name, &t.DailyUSDCap, &t.MonthlyUSDCap, &t.MaxConcurrent, &t.RPM, &disabled, &lastUsed, &created, &groupsJSON, &t.WorkspaceID, &t.AdminMonthlyCap); err != nil {
		return nil, err
	}
	t.Disabled = disabled != 0
	if lastUsed > 0 {
		t.LastUsedAt = time.Unix(lastUsed, 0)
	}
	t.CreatedAt = time.Unix(created, 0)
	t.Groups = parseGroupsJSON(groupsJSON)
	return &t, nil
}

// parseGroupsJSON decodes the groups column. Empty / malformed → nil slice.
func parseGroupsJSON(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	if err := jsonUnmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// marshalGroupsJSON encodes a groups slice for storage. Empty → "".
func marshalGroupsJSON(g []string) string {
	if len(g) == 0 {
		return ""
	}
	b, _ := jsonMarshal(g)
	return string(b)
}

type TokenParams struct {
	Name          string
	DailyUSDCap   float64
	MonthlyUSDCap float64
	MaxConcurrent int
	RPM           int
	Groups        []string
	// WorkspaceID is the billing target chosen at creation. 0 = bind to the
	// user's personal workspace (default for individual users).
	WorkspaceID int64
}

func (db *DB) CreateUserToken(ctx context.Context, userID int64, p TokenParams) (*UserToken, error) {
	tok, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	wsID := p.WorkspaceID
	if wsID == 0 {
		// Default: bind to the owner's personal workspace.
		if pw, perr := db.PersonalWorkspaceID(ctx, userID); perr == nil {
			wsID = pw
		}
	}
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx, `INSERT INTO user_tokens
		(user_id, token, name, daily_usd_cap, monthly_usd_cap, max_concurrent, rpm, disabled, last_used_at, created_at, groups, workspace_id, admin_monthly_cap)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?, 0)`,
		userID, tok, p.Name, p.DailyUSDCap, p.MonthlyUSDCap, p.MaxConcurrent, p.RPM, now, marshalGroupsJSON(p.Groups), wsID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.GetUserToken(ctx, id)
}

func (db *DB) GetUserToken(ctx context.Context, id int64) (*UserToken, error) {
	row := db.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM user_tokens WHERE id = ?`, id)
	t, err := scanToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (db *DB) GetUserTokenByValue(ctx context.Context, tok string) (*UserToken, error) {
	row := db.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM user_tokens WHERE token = ?`, tok)
	t, err := scanToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (db *DB) ListUserTokens(ctx context.Context, userID int64) ([]*UserToken, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+tokenCols+` FROM user_tokens WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*UserToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (db *DB) UpdateUserToken(ctx context.Context, id int64, p TokenParams, disabled *bool) error {
	groups := marshalGroupsJSON(p.Groups)
	if disabled != nil {
		d := 0
		if *disabled {
			d = 1
		}
		_, err := db.ExecContext(ctx, `UPDATE user_tokens SET name=?, daily_usd_cap=?, monthly_usd_cap=?, max_concurrent=?, rpm=?, disabled=?, groups=? WHERE id=?`,
			p.Name, p.DailyUSDCap, p.MonthlyUSDCap, p.MaxConcurrent, p.RPM, d, groups, id)
		return err
	}
	_, err := db.ExecContext(ctx, `UPDATE user_tokens SET name=?, daily_usd_cap=?, monthly_usd_cap=?, max_concurrent=?, rpm=?, groups=? WHERE id=?`,
		p.Name, p.DailyUSDCap, p.MonthlyUSDCap, p.MaxConcurrent, p.RPM, groups, id)
	return err
}

func (db *DB) DeleteUserToken(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM user_tokens WHERE id = ?`, id)
	return err
}

func (db *DB) RotateUserToken(ctx context.Context, id int64) (*UserToken, error) {
	tok, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `UPDATE user_tokens SET token = ? WHERE id = ?`, tok, id); err != nil {
		return nil, err
	}
	return db.GetUserToken(ctx, id)
}

func (db *DB) TouchUserToken(ctx context.Context, id int64) {
	_, _ = db.ExecContext(ctx, `UPDATE user_tokens SET last_used_at = ? WHERE id = ?`, time.Now().Unix(), id)
}

// GenerateToken returns a fresh token string of form "sk-cpa-<48 chars>".
func GenerateToken() (string, error) {
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	const n = 48
	maxV := big.NewInt(int64(len(alpha)))
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, maxV)
		if err != nil {
			return "", err
		}
		b[i] = alpha[v.Int64()]
	}
	return "sk-cpa-" + string(b), nil
}
