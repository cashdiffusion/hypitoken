package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GiftCard is one peer-to-peer wallet transfer held in escrow. The sender's
// balance is debited at send time (status=pending); the recipient claims it
// (status=claimed, their balance credited); an unclaimed gift past expires_at
// is swept back to the sender (status=refunded).
type GiftCard struct {
	ID              int64     `json:"id"`
	SenderUserID    int64     `json:"sender_user_id"`
	Code            string    `json:"code"`
	RecipientEmail  string    `json:"recipient_email"`
	AmountUSD       float64   `json:"amount_usd"`
	Message         string    `json:"message"`
	CardStyle       string    `json:"card_style"`
	CardTone        string    `json:"card_tone"`
	Status          string    `json:"status"`
	ClaimedByUserID int64     `json:"claimed_by_user_id"`
	ClaimedAt       time.Time `json:"claimed_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedAt       time.Time `json:"created_at"`
}

// Gift statuses.
const (
	GiftPending  = "pending"
	GiftClaimed  = "claimed"
	GiftExpired  = "expired"
	GiftRefunded = "refunded"
)

// ErrGiftNotClaimable is returned when a claim/refund targets a gift that is no
// longer pending (already claimed, expired, or refunded). Callers treat it as a
// benign no-op so a double-claim race can never credit twice.
var ErrGiftNotClaimable = errors.New("gift is not claimable")

const giftCols = `id, sender_user_id, code, recipient_email, amount_usd, message, card_style, card_tone, status, claimed_by_user_id, claimed_at, expires_at, created_at`

func scanGift(row interface{ Scan(...any) error }) (*GiftCard, error) {
	var g GiftCard
	var claimedAt, expiresAt, createdAt int64
	if err := row.Scan(&g.ID, &g.SenderUserID, &g.Code, &g.RecipientEmail, &g.AmountUSD, &g.Message,
		&g.CardStyle, &g.CardTone, &g.Status, &g.ClaimedByUserID, &claimedAt, &expiresAt, &createdAt); err != nil {
		return nil, err
	}
	if claimedAt > 0 {
		g.ClaimedAt = time.Unix(claimedAt, 0)
	}
	if expiresAt > 0 {
		g.ExpiresAt = time.Unix(expiresAt, 0)
	}
	g.CreatedAt = time.Unix(createdAt, 0)
	return &g, nil
}

// GiftSendTx atomically debits the sender's wallet and writes the pending gift
// escrow row in one transaction — so we can never debit without recording the
// gift (or vice-versa). g must carry SenderUserID, Code, RecipientEmail,
// AmountUSD, Message, CardStyle, CardTone and ExpiresAt. Returns the sender's
// new balance. ErrInsufficientBalance when the sender can't cover the gift.
func (db *DB) GiftSendTx(ctx context.Context, g GiftCard) (newSenderBal float64, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	bal, err := AddBalanceTx(ctx, tx, g.SenderUserID, TxKindAdjust, -g.AmountUSD,
		"gift_send:"+g.Code, "赠送礼品卡给 "+g.RecipientEmail, false)
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	var expiresAt int64
	if !g.ExpiresAt.IsZero() {
		expiresAt = g.ExpiresAt.Unix()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gift_cards
		(sender_user_id, code, recipient_email, amount_usd, message, card_style, card_tone, status, claimed_by_user_id, claimed_at, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?)`,
		g.SenderUserID, g.Code, g.RecipientEmail, g.AmountUSD, g.Message, g.CardStyle, g.CardTone, GiftPending, expiresAt, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return bal, nil
}

// GiftClaimTx atomically flips a pending gift to claimed and credits the
// recipient's wallet. Idempotent against double-claim: the status guard means a
// second concurrent call sees 0 rows affected and returns ErrGiftNotClaimable
// without touching any wallet. Returns the updated gift + recipient's new
// balance.
func (db *DB) GiftClaimTx(ctx context.Context, code string, recipientUserID int64) (*GiftCard, float64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	g, err := scanGift(tx.QueryRowContext(ctx, `SELECT `+giftCols+` FROM gift_cards WHERE code = ?`, code))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	if g.Status != GiftPending {
		return nil, 0, ErrGiftNotClaimable
	}
	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, `UPDATE gift_cards SET status = ?, claimed_by_user_id = ?, claimed_at = ? WHERE code = ? AND status = ?`,
		GiftClaimed, recipientUserID, now, code, GiftPending)
	if err != nil {
		return nil, 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, 0, ErrGiftNotClaimable
	}
	bal, err := AddBalanceTx(ctx, tx, recipientUserID, TxKindAdjust, g.AmountUSD,
		"gift_claim:"+code, "收到礼品卡赠额", true)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	g.Status = GiftClaimed
	g.ClaimedByUserID = recipientUserID
	g.ClaimedAt = time.Unix(now, 0)
	return g, bal, nil
}

// GiftRefundTx atomically flips a pending gift to refunded and credits the
// amount back to the sender. Same idempotency guard as the claim path.
func (db *DB) GiftRefundTx(ctx context.Context, code string) (*GiftCard, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	g, err := scanGift(tx.QueryRowContext(ctx, `SELECT `+giftCols+` FROM gift_cards WHERE code = ?`, code))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if g.Status != GiftPending {
		return nil, ErrGiftNotClaimable
	}
	res, err := tx.ExecContext(ctx, `UPDATE gift_cards SET status = ? WHERE code = ? AND status = ?`,
		GiftRefunded, code, GiftPending)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrGiftNotClaimable
	}
	if _, err := AddBalanceTx(ctx, tx, g.SenderUserID, TxKindRefund, g.AmountUSD,
		"gift_refund:"+code, "礼品卡过期退回", true); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	g.Status = GiftRefunded
	return g, nil
}

// GetGiftByCode looks up one gift by its redeem code (case handled by caller).
func (db *DB) GetGiftByCode(ctx context.Context, code string) (*GiftCard, error) {
	g, err := scanGift(db.QueryRowContext(ctx, `SELECT `+giftCols+` FROM gift_cards WHERE code = ?`, code))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return g, err
}

func giftPage(rows *sql.Rows) ([]*GiftCard, error) {
	defer rows.Close()
	var out []*GiftCard
	for rows.Next() {
		g, err := scanGift(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListGiftsBySender returns a page of gifts a user has sent (newest first).
func (db *DB) ListGiftsBySender(ctx context.Context, senderID int64, limit, offset int) ([]*GiftCard, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gift_cards WHERE sender_user_id = ?`, senderID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+giftCols+` FROM gift_cards WHERE sender_user_id = ? ORDER BY id DESC LIMIT ? OFFSET ?`, senderID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	out, err := giftPage(rows)
	return out, total, err
}

// ListGiftsForRecipientEmail returns gifts addressed to an email (the "received"
// view). Includes pending (claimable) and claimed history, newest first.
func (db *DB) ListGiftsForRecipientEmail(ctx context.Context, email string, limit, offset int) ([]*GiftCard, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gift_cards WHERE recipient_email = ?`, email).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+giftCols+` FROM gift_cards WHERE recipient_email = ? ORDER BY id DESC LIMIT ? OFFSET ?`, email, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	out, err := giftPage(rows)
	return out, total, err
}

// ListPendingGiftsForEmail returns every pending gift addressed to an email —
// used to auto-claim on registration / email verification.
func (db *DB) ListPendingGiftsForEmail(ctx context.Context, email string) ([]*GiftCard, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+giftCols+` FROM gift_cards WHERE recipient_email = ? AND status = ?`, email, GiftPending)
	if err != nil {
		return nil, err
	}
	return giftPage(rows)
}

// ListExpiredPendingGifts returns pending gifts whose expiry has passed — the
// sweeper refunds each one.
func (db *DB) ListExpiredPendingGifts(ctx context.Context, now time.Time) ([]*GiftCard, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+giftCols+` FROM gift_cards WHERE status = ? AND expires_at > 0 AND expires_at <= ?`, GiftPending, now.Unix())
	if err != nil {
		return nil, err
	}
	return giftPage(rows)
}

// GiftTotals is the operator-facing gift rollup for the referral ops dashboard.
type GiftTotals struct {
	SentCount     int64   `json:"sent_count"`
	SentUSD       float64 `json:"sent_usd"`
	ClaimedCount  int64   `json:"claimed_count"`
	ClaimedUSD    float64 `json:"claimed_usd"`
	PendingCount  int64   `json:"pending_count"`
	PendingUSD    float64 `json:"pending_usd"`
	RefundedCount int64   `json:"refunded_count"`
	RefundedUSD   float64 `json:"refunded_usd"`
}

// GiftTotalsAll aggregates the gift_cards table for the admin analytics view.
func (db *DB) GiftTotalsAll(ctx context.Context) (*GiftTotals, error) {
	var t GiftTotals
	err := db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(amount_usd),0),
			COALESCE(SUM(CASE WHEN status='claimed'  THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='claimed'  THEN amount_usd ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='pending'  THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='pending'  THEN amount_usd ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='refunded' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='refunded' THEN amount_usd ELSE 0 END),0)
		FROM gift_cards`).Scan(
		&t.SentCount, &t.SentUSD, &t.ClaimedCount, &t.ClaimedUSD,
		&t.PendingCount, &t.PendingUSD, &t.RefundedCount, &t.RefundedUSD)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
