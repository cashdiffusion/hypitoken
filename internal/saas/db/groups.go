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

// NOTE: pricing-group multiplier management (CreateGroup/UpdateGroup/DeleteGroup)
// was removed when billing multipliers moved onto the workspace. The remaining
// readers (GetGroup, ListGroups, DefaultGroup) only support the frozen
// users.group_id FK + registration default; nothing here drives billing.
