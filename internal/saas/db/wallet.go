package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	TxKindTopup  = "topup"
	TxKindCharge = "charge"
	TxKindAdjust = "adjust"
	TxKindRefund = "refund"
)

type WalletTx struct {
	ID         int64
	UserID     int64
	Kind       string
	AmountUSD  float64
	Ref        string
	Note       string
	CreatedAt  time.Time
}

var ErrInsufficientBalance = errors.New("insufficient balance")

// AddBalance applies a signed delta to the user's balance and records a wallet_tx
// row in a single transaction. Returns the new balance.
//
// allowNegative=false rejects writes that would push the balance below zero
// (typical for charge/adjust-down). allowNegative=true is for admin force
// adjustments.
func (db *DB) AddBalance(ctx context.Context, userID int64, kind string, deltaUSD float64, ref, note string, allowNegative bool) (newBal float64, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var bal float64
	if err := tx.QueryRowContext(ctx, `SELECT balance_usd FROM users WHERE id = ?`, userID).Scan(&bal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	bal += deltaUSD
	if bal < 0 && !allowNegative {
		return 0, ErrInsufficientBalance
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET balance_usd = ?, updated_at = ? WHERE id = ?`, bal, now, userID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO wallet_tx (user_id, kind, amount_usd, ref, note, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, kind, deltaUSD, ref, note, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return bal, nil
}

func (db *DB) GetBalance(ctx context.Context, userID int64) (float64, error) {
	var bal float64
	err := db.QueryRowContext(ctx, `SELECT balance_usd FROM users WHERE id = ?`, userID).Scan(&bal)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return bal, err
}

// ListWalletTx returns recent transactions for a user.
func (db *DB) ListWalletTx(ctx context.Context, userID int64, limit int) ([]*WalletTx, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `SELECT id, user_id, kind, amount_usd, ref, note, created_at FROM wallet_tx WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WalletTx
	for rows.Next() {
		var t WalletTx
		var c int64
		if err := rows.Scan(&t.ID, &t.UserID, &t.Kind, &t.AmountUSD, &t.Ref, &t.Note, &c); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(c, 0)
		out = append(out, &t)
	}
	return out, rows.Err()
}

// FleetWalletTotals is the aggregate summary used by the operator console
// to compare fleet-wide upstream cost against what users actually paid us
// (the latter reflects per-group multipliers and is the truer
// "we-saved-them-money" number).
type FleetWalletTotals struct {
	UserPaidUSD   float64 // sum of -amount_usd, kind='charge', all users
	TopupsUSD     float64 // sum of  amount_usd, kind='topup',  all users
	ChargeCount   int64
}

// FleetTotals aggregates wallet movement across every SaaS user. Cheap
// table scan — wallet_tx has at most one row per request, indexed on
// user_id but we scan unfiltered here.
func (db *DB) FleetTotals(ctx context.Context) (*FleetWalletTotals, error) {
	row := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN kind='charge' THEN -amount_usd ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind='topup'  THEN  amount_usd ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind='charge' THEN 1 ELSE 0 END), 0)
		FROM wallet_tx`)
	var t FleetWalletTotals
	if err := row.Scan(&t.UserPaidUSD, &t.TopupsUSD, &t.ChargeCount); err != nil {
		return nil, err
	}
	return &t, nil
}

// SumChargeSince returns total absolute USD charged from this user's wallet
// since `since`. Useful for daily/monthly usage caps on tokens.
func (db *DB) SumChargeSince(ctx context.Context, userID int64, since time.Time) (float64, error) {
	var sum sql.NullFloat64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(-amount_usd), 0) FROM wallet_tx WHERE user_id = ? AND kind = 'charge' AND created_at >= ?`,
		userID, since.Unix()).Scan(&sum)
	if err != nil {
		return 0, err
	}
	return sum.Float64, nil
}
