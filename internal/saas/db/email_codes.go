package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	PurposeVerify = "verify"
	PurposeReset  = "reset"
	// PurposeAppeal proves ownership of an address for a support appeal filed
	// without a session — the only channel a disabled account has left.
	PurposeAppeal = "appeal"
)

// PutEmailCode stores (or replaces) a verification code for (email, purpose)
// with the given TTL. Caller is responsible for sending the code over SMTP.
func (db *DB) PutEmailCode(ctx context.Context, email, code, purpose string, ttl time.Duration) error {
	email = strings.ToLower(strings.TrimSpace(email))
	now := time.Now()
	_, err := db.ExecContext(ctx, `INSERT INTO email_codes (email, code, purpose, expires_at, attempts, created_at)
		VALUES (?, ?, ?, ?, 0, ?)
		ON CONFLICT(email, purpose) DO UPDATE SET code = excluded.code, expires_at = excluded.expires_at, attempts = 0, created_at = excluded.created_at`,
		email, code, purpose, now.Add(ttl).Unix(), now.Unix())
	return err
}

// ConsumeEmailCode verifies and deletes the code if it matches and hasn't
// expired. Increments attempts on mismatch and rejects after 5.
func (db *DB) ConsumeEmailCode(ctx context.Context, email, code, purpose string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var stored string
	var exp int64
	var attempts int
	err = tx.QueryRowContext(ctx, `SELECT code, expires_at, attempts FROM email_codes WHERE email = ? AND purpose = ?`, email, purpose).Scan(&stored, &exp, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("no code requested")
	}
	if err != nil {
		return err
	}
	if time.Now().Unix() > exp {
		_, _ = tx.ExecContext(ctx, `DELETE FROM email_codes WHERE email = ? AND purpose = ?`, email, purpose)
		_ = tx.Commit()
		return errors.New("code expired")
	}
	if attempts >= 5 {
		return errors.New("too many attempts")
	}
	if stored != code {
		_, _ = tx.ExecContext(ctx, `UPDATE email_codes SET attempts = attempts + 1 WHERE email = ? AND purpose = ?`, email, purpose)
		_ = tx.Commit()
		return errors.New("invalid code")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM email_codes WHERE email = ? AND purpose = ?`, email, purpose); err != nil {
		return err
	}
	return tx.Commit()
}

// GenerateNumericCode returns a zero-padded random numeric string of len digits.
func GenerateNumericCode(digits int) (string, error) {
	if digits <= 0 {
		digits = 6
	}
	var sb strings.Builder
	for i := 0; i < digits; i++ {
		v, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&sb, "%d", v.Int64())
	}
	return sb.String(), nil
}
