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

func (db *DB) ListOrders(ctx context.Context, userID int64, limit int) ([]*AlipayOrder, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `SELECT `+orderCols+` FROM alipay_orders WHERE user_id = ? ORDER BY created_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AlipayOrder
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (db *DB) ListAllOrders(ctx context.Context, limit int) ([]*AlipayOrder, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `SELECT `+orderCols+` FROM alipay_orders ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AlipayOrder
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
