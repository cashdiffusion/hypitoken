package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	OrderPending = "pending"
	OrderPaid    = "paid"
	OrderExpired = "expired"
	OrderFailed  = "failed"
)

type AlipayOrder struct {
	OutTradeNo string
	UserID     int64
	CNYAmount  float64
	USDCredit  float64
	Rate       float64
	Status     string
	TradeNo    string
	QRCode     string
	CreatedAt  time.Time
	PaidAt     time.Time
}

const orderCols = `out_trade_no, user_id, cny_amount, usd_credit, rate, status, trade_no, qr_code, created_at, paid_at`

func scanOrder(row interface{ Scan(...any) error }) (*AlipayOrder, error) {
	var o AlipayOrder
	var c, p int64
	if err := row.Scan(&o.OutTradeNo, &o.UserID, &o.CNYAmount, &o.USDCredit, &o.Rate, &o.Status, &o.TradeNo, &o.QRCode, &c, &p); err != nil {
		return nil, err
	}
	o.CreatedAt = time.Unix(c, 0)
	if p > 0 {
		o.PaidAt = time.Unix(p, 0)
	}
	return &o, nil
}

func (db *DB) CreateOrder(ctx context.Context, o AlipayOrder) error {
	o.CreatedAt = time.Now()
	_, err := db.ExecContext(ctx, `INSERT INTO alipay_orders
		(out_trade_no, user_id, cny_amount, usd_credit, rate, status, trade_no, qr_code, created_at, paid_at)
		VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, 0)`,
		o.OutTradeNo, o.UserID, o.CNYAmount, o.USDCredit, o.Rate, OrderPending, o.QRCode, o.CreatedAt.Unix())
	return err
}

func (db *DB) GetOrder(ctx context.Context, outTradeNo string) (*AlipayOrder, error) {
	row := db.QueryRowContext(ctx, `SELECT `+orderCols+` FROM alipay_orders WHERE out_trade_no = ?`, outTradeNo)
	o, err := scanOrder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

func (db *DB) MarkOrderPaid(ctx context.Context, outTradeNo, tradeNo string) error {
	res, err := db.ExecContext(ctx, `UPDATE alipay_orders SET status = ?, trade_no = ?, paid_at = ? WHERE out_trade_no = ? AND status = ?`,
		OrderPaid, tradeNo, time.Now().Unix(), outTradeNo, OrderPending)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("order not pending")
	}
	return nil
}

// ErrOrderNotPending is returned by CreditPaidOrder when the order isn't in
// the pending state any more — i.e. a concurrent webhook already credited
// it. Callers should treat it as a successful no-op.
var ErrOrderNotPending = errors.New("order not pending")

// CreditPaidOrder performs the two state changes that make a top-up real —
// marking the alipay order paid and adding the USD credit to the user's
// balance — atomically in a single transaction. Either both happen or
// neither does, so we can't end up with a "paid but not credited" zombie
// order if the process crashes between the two steps.
//
// Idempotent against webhook replays: the UPDATE is gated on
// `status = pending`, so a second concurrent call sees 0 rows affected and
// returns ErrOrderNotPending without touching the wallet.
//
// Returns the new balance on success.
func (db *DB) CreditPaidOrder(ctx context.Context, outTradeNo, tradeNo string, userID int64, usdCredit float64, ref, note string) (float64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()

	// Step 1 — flip pending → paid. Conditional update keeps replays safe.
	res, err := tx.ExecContext(ctx, `UPDATE alipay_orders SET status = ?, trade_no = ?, paid_at = ? WHERE out_trade_no = ? AND status = ?`,
		OrderPaid, tradeNo, now, outTradeNo, OrderPending)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrOrderNotPending
	}

	// Step 2 — read current balance under the same transaction (so a
	// concurrent charge can't race the read/write), apply the delta, write
	// both the balance and the wallet_tx ledger row.
	var bal float64
	if err := tx.QueryRowContext(ctx, `SELECT balance_usd FROM users WHERE id = ?`, userID).Scan(&bal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	bal += usdCredit
	if _, err := tx.ExecContext(ctx, `UPDATE users SET balance_usd = ?, updated_at = ? WHERE id = ?`, bal, now, userID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO wallet_tx (user_id, kind, amount_usd, ref, note, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, TxKindTopup, usdCredit, ref, note, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return bal, nil
}

// CreditStripeOrder is the Stripe analogue of CreditPaidOrder. The settlement
// source is an amount-verified PaymentIntent (retrieved live or delivered by a
// signature-checked webhook), which is authoritative — so unlike the Alipay
// path it credits an order that is pending OR expired. A slow redirect-based
// method (Alipay / WeChat / crypto via Stripe) can settle after the 15-minute
// sweeper already flipped the order to expired; without this the money would be
// taken but never credited. Still fully idempotent: a paid order is a no-op
// returning ErrOrderNotPending, so webhook + poll racing can't double-credit.
func (db *DB) CreditStripeOrder(ctx context.Context, outTradeNo, tradeNo string, userID int64, usdCredit float64, ref, note string) (float64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()

	// Flip (pending|expired) → paid. The `status != 'paid'` guard keeps the
	// double-credit window closed while still rescuing expired orders.
	res, err := tx.ExecContext(ctx, `UPDATE alipay_orders SET status = ?, trade_no = ?, paid_at = ? WHERE out_trade_no = ? AND status != ?`,
		OrderPaid, tradeNo, now, outTradeNo, OrderPaid)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrOrderNotPending
	}

	var bal float64
	if err := tx.QueryRowContext(ctx, `SELECT balance_usd FROM users WHERE id = ?`, userID).Scan(&bal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	bal += usdCredit
	if _, err := tx.ExecContext(ctx, `UPDATE users SET balance_usd = ?, updated_at = ? WHERE id = ?`, bal, now, userID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO wallet_tx (user_id, kind, amount_usd, ref, note, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, TxKindTopup, usdCredit, ref, note, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return bal, nil
}

func (db *DB) MarkOrderExpired(ctx context.Context, outTradeNo string) error {
	_, err := db.ExecContext(ctx, `UPDATE alipay_orders SET status = ? WHERE out_trade_no = ? AND status = ?`,
		OrderExpired, outTradeNo, OrderPending)
	return err
}

// ExpirePendingOrdersBefore atomically transitions every pending order
// created before `cutoff` to expired. Returns the count touched.
func (db *DB) ExpirePendingOrdersBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE alipay_orders SET status = ? WHERE status = ? AND created_at < ?`,
		OrderExpired, OrderPending, cutoff.Unix())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// CountPendingOrders returns the number of pending orders for a user. Used
// by the topup handler to refuse new orders when too many are in flight.
func (db *DB) CountPendingOrders(ctx context.Context, userID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM alipay_orders WHERE user_id = ? AND status = ?`,
		userID, OrderPending).Scan(&n)
	return n, err
}

// ListOrders returns a page of the user's orders plus the user's total
// order count (for pagination).
func (db *DB) ListOrders(ctx context.Context, userID int64, limit, offset int) ([]*AlipayOrder, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alipay_orders WHERE user_id = ?`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+orderCols+` FROM alipay_orders WHERE user_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*AlipayOrder
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	return out, total, rows.Err()
}

// ListAllOrders returns a page of all orders plus the total order count
// (for pagination).
func (db *DB) ListAllOrders(ctx context.Context, limit, offset int) ([]*AlipayOrder, int, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alipay_orders`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+orderCols+` FROM alipay_orders ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*AlipayOrder
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, o)
	}
	return out, total, rows.Err()
}
