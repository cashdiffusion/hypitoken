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
	ID        int64
	UserID    int64
	Kind      string
	AmountUSD float64
	Ref       string
	Note      string
	CreatedAt time.Time
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
	UserPaidUSD float64 // sum of -amount_usd, kind='charge', all users
	TopupsUSD   float64 // sum of  amount_usd, kind='topup',  all users
	ChargeCount int64
}

// AdminDashboardSnapshot is the composite aggregate the admin dashboard
// renders on first paint — one query trip rather than N round-tripping
// counts/SUMs from the browser.
type AdminDashboardSnapshot struct {
	UsersTotal         int64
	UsersVerified      int64
	UsersNew30d        int64
	UsersDisabled      int64
	TopupsLifetime     float64
	Topups30d          float64
	Topups7d           float64
	ChargesLifetime    float64
	Charges30d         float64
	Charges7d          float64
	BalanceOutstanding float64
	OrdersPending      int64
	OrdersPaidLifetime int64
	DailyRevenue14d    []DailyAmount // last 14 days, oldest first
	TopSpenders        []UserSpend   // top 5 by lifetime charge
	RecentTopups       []OrderRow    // last 10 paid orders
	RecentSignups      []UserRow     // last 5 users
}

type DailyAmount struct {
	Day    string // YYYY-MM-DD UTC
	Amount float64
}

type UserSpend struct {
	UserID int64
	Email  string
	Spent  float64
}

type OrderRow struct {
	OutTradeNo string
	UserID     int64
	UserEmail  string
	CNYAmount  float64
	USDCredit  float64
	PaidAt     time.Time
}

type UserRow struct {
	ID        int64
	Email     string
	Role      string
	Verified  bool
	Disabled  bool
	CreatedAt time.Time
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

// AdminDashboard returns the composite snapshot used by the admin
// dashboard tab. One funcall instead of ten — the dashboard does an
// initial paint that depends on most of these fields, so a single
// transaction-free batch reduces the latency floor on the first paint.
func (db *DB) AdminDashboard(ctx context.Context) (*AdminDashboardSnapshot, error) {
	now := time.Now()
	d30 := now.Add(-30 * 24 * time.Hour).Unix()
	d7 := now.Add(-7 * 24 * time.Hour).Unix()
	snap := &AdminDashboardSnapshot{}

	// Users
	if err := db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN email_verified=1 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN disabled=1 THEN 1 ELSE 0 END),0),
			COALESCE(SUM(balance_usd),0)
		FROM users`, d30).
		Scan(&snap.UsersTotal, &snap.UsersVerified, &snap.UsersNew30d,
			&snap.UsersDisabled, &snap.BalanceOutstanding); err != nil {
		return nil, err
	}

	// Wallet rollup (lifetime / 30d / 7d for both topups and charges).
	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN kind='topup'  THEN  amount_usd ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN kind='topup'  AND created_at >= ? THEN  amount_usd ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN kind='topup'  AND created_at >= ? THEN  amount_usd ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN kind='charge' THEN -amount_usd ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN kind='charge' AND created_at >= ? THEN -amount_usd ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN kind='charge' AND created_at >= ? THEN -amount_usd ELSE 0 END),0)
		FROM wallet_tx`,
		d30, d7, d30, d7).
		Scan(&snap.TopupsLifetime, &snap.Topups30d, &snap.Topups7d,
			&snap.ChargesLifetime, &snap.Charges30d, &snap.Charges7d); err != nil {
		return nil, err
	}

	// Order rollup
	if err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN status='pending' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='paid'    THEN 1 ELSE 0 END),0)
		FROM alipay_orders`).Scan(&snap.OrdersPending, &snap.OrdersPaidLifetime); err != nil {
		return nil, err
	}

	// Daily revenue last 14 days (UTC). Pre-fill zeros so the chart
	// doesn't need to know about missing keys.
	{
		days := make([]DailyAmount, 14)
		dayKey := func(t time.Time) string { return t.UTC().Format("2006-01-02") }
		for i := 0; i < 14; i++ {
			t := now.Add(time.Duration(-(13 - i)) * 24 * time.Hour)
			days[i] = DailyAmount{Day: dayKey(t)}
		}
		idx := map[string]int{}
		for i, d := range days {
			idx[d.Day] = i
		}
		from := now.Add(-14 * 24 * time.Hour).Unix()
		rows, err := db.QueryContext(ctx, `
			SELECT strftime('%Y-%m-%d', created_at, 'unixepoch') AS day,
			       COALESCE(SUM(amount_usd),0) AS amt
			  FROM wallet_tx
			 WHERE kind='topup' AND created_at >= ?
			 GROUP BY day`, from)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var day string
			var amt float64
			if err := rows.Scan(&day, &amt); err != nil {
				rows.Close()
				return nil, err
			}
			if i, ok := idx[day]; ok {
				days[i].Amount = amt
			}
		}
		rows.Close()
		snap.DailyRevenue14d = days
	}

	// Top spenders (lifetime charges)
	{
		rows, err := db.QueryContext(ctx, `
			SELECT u.id, u.email, COALESCE(SUM(-w.amount_usd),0) AS spent
			  FROM users u JOIN wallet_tx w ON w.user_id=u.id AND w.kind='charge'
			 GROUP BY u.id
			 ORDER BY spent DESC
			 LIMIT 5`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var u UserSpend
			if err := rows.Scan(&u.UserID, &u.Email, &u.Spent); err != nil {
				rows.Close()
				return nil, err
			}
			snap.TopSpenders = append(snap.TopSpenders, u)
		}
		rows.Close()
	}

	// Recent paid orders
	{
		rows, err := db.QueryContext(ctx, `
			SELECT o.out_trade_no, o.user_id, COALESCE(u.email,'') , o.cny_amount, o.usd_credit, o.paid_at
			  FROM alipay_orders o LEFT JOIN users u ON u.id=o.user_id
			 WHERE o.status='paid'
			 ORDER BY o.paid_at DESC
			 LIMIT 10`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var o OrderRow
			var paidAt int64
			if err := rows.Scan(&o.OutTradeNo, &o.UserID, &o.UserEmail, &o.CNYAmount, &o.USDCredit, &paidAt); err != nil {
				rows.Close()
				return nil, err
			}
			if paidAt > 0 {
				o.PaidAt = time.Unix(paidAt, 0)
			}
			snap.RecentTopups = append(snap.RecentTopups, o)
		}
		rows.Close()
	}

	// Recent signups
	{
		rows, err := db.QueryContext(ctx, `
			SELECT id, email, role, email_verified, disabled, created_at
			  FROM users ORDER BY created_at DESC LIMIT 5`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var u UserRow
			var created int64
			var ver, dis int
			if err := rows.Scan(&u.ID, &u.Email, &u.Role, &ver, &dis, &created); err != nil {
				rows.Close()
				return nil, err
			}
			u.Verified = ver == 1
			u.Disabled = dis == 1
			u.CreatedAt = time.Unix(created, 0)
			snap.RecentSignups = append(snap.RecentSignups, u)
		}
		rows.Close()
	}

	return snap, nil
}
