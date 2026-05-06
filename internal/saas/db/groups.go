package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Default multipliers used when CreateGroup is called with zero values.
// Match the migration v3 UPDATE that retroactively bumps existing default
// rows: 0.3 for Claude, 0.05 for Codex.
const (
	DefaultClaudeMultiplier = 0.3
	DefaultCodexMultiplier  = 0.05
)

type PricingGroup struct {
	ID          int64
	Name        string
	Description string
	// codex_rmb_per_usd / claude_rmb_per_usd remain in the schema for
	// backward compatibility but are no longer read or written by Go code.
	// The single source of truth is now the per-provider multiplier:
	//   final_charge_USD = official_USD * multiplier
	CodexMultiplier  float64
	ClaudeMultiplier float64
	CredentialGroup  string // forwarded to auth.Pool group filter
	IsDefault        bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const groupCols = `id, name, description, codex_multiplier, claude_multiplier, credential_group, is_default, created_at, updated_at`

func scanGroup(row interface{ Scan(...any) error }) (*PricingGroup, error) {
	var g PricingGroup
	var isDefault int
	var c, u int64
	if err := row.Scan(&g.ID, &g.Name, &g.Description, &g.CodexMultiplier, &g.ClaudeMultiplier, &g.CredentialGroup, &isDefault, &c, &u); err != nil {
		return nil, err
	}
	g.IsDefault = isDefault != 0
	g.CreatedAt = time.Unix(c, 0)
	g.UpdatedAt = time.Unix(u, 0)
	return &g, nil
}

func (db *DB) GetGroup(ctx context.Context, id int64) (*PricingGroup, error) {
	row := db.QueryRowContext(ctx, `SELECT `+groupCols+` FROM pricing_groups WHERE id = ?`, id)
	g, err := scanGroup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return g, err
}

func (db *DB) DefaultGroup(ctx context.Context) (*PricingGroup, error) {
	row := db.QueryRowContext(ctx, `SELECT `+groupCols+` FROM pricing_groups WHERE is_default = 1 LIMIT 1`)
	g, err := scanGroup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return g, err
}

func (db *DB) ListGroups(ctx context.Context) ([]*PricingGroup, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+groupCols+` FROM pricing_groups ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PricingGroup
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

type GroupParams struct {
	Name             string
	Description      string
	CodexMultiplier  float64
	ClaudeMultiplier float64
	CredentialGroup  string
}

// applyMultiplierDefaults backfills zero multipliers with the package
// defaults so a freshly-created group never starts at 0× (which would
// silently zero out billing).
func (p *GroupParams) applyMultiplierDefaults() {
	if p.ClaudeMultiplier <= 0 {
		p.ClaudeMultiplier = DefaultClaudeMultiplier
	}
	if p.CodexMultiplier <= 0 {
		p.CodexMultiplier = DefaultCodexMultiplier
	}
}

func (db *DB) CreateGroup(ctx context.Context, p GroupParams) (*PricingGroup, error) {
	p.applyMultiplierDefaults()
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx, `INSERT INTO pricing_groups
		(name, description, codex_multiplier, claude_multiplier, credential_group, is_default, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?)`,
		p.Name, p.Description, p.CodexMultiplier, p.ClaudeMultiplier, p.CredentialGroup, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.GetGroup(ctx, id)
}

func (db *DB) UpdateGroup(ctx context.Context, id int64, p GroupParams) (*PricingGroup, error) {
	p.applyMultiplierDefaults()
	_, err := db.ExecContext(ctx, `UPDATE pricing_groups SET
		name = ?, description = ?, codex_multiplier = ?, claude_multiplier = ?,
		credential_group = ?, updated_at = ?
		WHERE id = ?`,
		p.Name, p.Description, p.CodexMultiplier, p.ClaudeMultiplier, p.CredentialGroup, time.Now().Unix(), id)
	if err != nil {
		return nil, err
	}
	return db.GetGroup(ctx, id)
}

func (db *DB) DeleteGroup(ctx context.Context, id int64) error {
	g, err := db.GetGroup(ctx, id)
	if err != nil {
		return err
	}
	if g.IsDefault {
		return errors.New("cannot delete default group")
	}
	def, err := db.DefaultGroup(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET group_id = ? WHERE group_id = ?`, def.ID, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pricing_groups WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
