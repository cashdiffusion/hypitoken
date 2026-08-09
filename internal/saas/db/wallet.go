package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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

// ChargeMeta is the structured attribution recorded alongside a charge row: which
// key spent the money, on which model, and how many tokens it moved. Before v15
// the first two were squeezed into the free-text `ref` and the token counts lived
// only in the retention-GC'd requestlog, which made "what did each key in my
// company spend" unanswerable from the ledger. Now the ledger is self-contained.
//
// The zero value is correct for money-in (topup / adjust / refund): those rows
// have no key or model, and land on the column defaults.
//
// Flat int64s rather than a cc-core usage.Counts, to keep package db free of any
// cc-core dependency.
type ChargeMeta struct {
	TokenID           int64
	Model             string
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheCreateTokens int64
}

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
	bal, err := AddBalanceTx(ctx, tx, userID, kind, deltaUSD, ref, note, allowNegative)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return bal, nil
}

// AddBalanceTx is the transaction-scoped core of AddBalance: it reads the
// balance, applies the signed delta, and writes both the users row and the
// wallet_tx ledger entry using the caller-supplied transaction. It does NOT
// commit — the caller owns the transaction lifecycle. This lets a feature
// (e.g. a gift transfer) compose a balance move with its own table writes in a
// single atomic unit, so we can't end up with money moved but the gift row
// unrecorded if the process crashes between the two. Returns the new balance.
func AddBalanceTx(ctx context.Context, tx *sql.Tx, userID int64, kind string, deltaUSD float64, ref, note string, allowNegative bool) (float64, error) {
	// User-centric money-in (topup / adjust / bonus / gift) always credits the
	// user's PERSONAL workspace — the home wallet that used to be users.balance_usd.
	wsID, err := personalWorkspaceIDTx(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	return addWorkspaceBalanceTx(ctx, tx, wsID, userID, kind, deltaUSD, ref, note, allowNegative)
}

// addWorkspaceBalanceTx applies a signed delta to a workspace's balance and
// records a wallet_tx row (attributed to userID) within the caller's tx.
func addWorkspaceBalanceTx(ctx context.Context, tx *sql.Tx, workspaceID, userID int64, kind string, deltaUSD float64, ref, note string, allowNegative bool) (float64, error) {
	var bal float64
	if err := tx.QueryRowContext(ctx, `SELECT balance_usd FROM workspaces WHERE id = ?`, workspaceID).Scan(&bal); err != nil {
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
	if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET balance_usd = ?, updated_at = ? WHERE id = ?`, bal, now, workspaceID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO wallet_tx (user_id, workspace_id, kind, amount_usd, ref, note, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, workspaceID, kind, deltaUSD, ref, note, now); err != nil {
		return 0, err
	}
	return bal, nil
}

// ChargeWithFloor deducts from the USER's personal workspace (the legacy
// "charge my own wallet" path). The proxy hot path uses ChargeWorkspaceWithFloor
// with the token's bound workspace instead.
func (db *DB) ChargeWithFloor(ctx context.Context, userID int64, kind string, amountUSD float64, ref, note string, maxOverdraftUSD float64, meta ChargeMeta) (newBal float64, charged float64, err error) {
	wsID, err := db.PersonalWorkspaceID(ctx, userID)
	if err != nil {
		return 0, 0, err
	}
	return db.ChargeWorkspaceWithFloor(ctx, wsID, userID, kind, amountUSD, ref, note, maxOverdraftUSD, meta)
}

// ChargeWorkspaceWithFloor deducts amountUSD (>= 0) from a workspace's balance
// pool in a single transaction, recording a wallet_tx of the given kind with a
// negative amount, attributed to userID (the member who triggered the request).
// The balance may go negative — a request that already hit upstream must be
// billed — but never below -maxOverdraftUSD: a charge that would breach that
// floor is clamped so the balance rests exactly on it, and the clamped
// (actually-charged) amount is returned so the request log stays in lockstep
// with the ledger. maxOverdraftUSD <= 0 disables the floor.
func (db *DB) ChargeWorkspaceWithFloor(ctx context.Context, workspaceID, userID int64, kind string, amountUSD float64, ref, note string, maxOverdraftUSD float64, meta ChargeMeta) (newBal float64, charged float64, err error) {
	if amountUSD <= 0 {
		bal, gerr := db.GetWorkspaceBalance(ctx, workspaceID)
		return bal, 0, gerr
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	var bal float64
	if err := tx.QueryRowContext(ctx, `SELECT balance_usd FROM workspaces WHERE id = ?`, workspaceID).Scan(&bal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, ErrNotFound
		}
		return 0, 0, err
	}

	charged = amountUSD
	if maxOverdraftUSD > 0 {
		// Clamp so the post-charge balance never drops below the floor. If the
		// wallet is already at/below the floor, charge nothing.
		if room := bal + maxOverdraftUSD; charged > room {
			charged = room
		}
		if charged < 0 {
			charged = 0
		}
	}
	newBal = bal - charged
	if charged == 0 {
		return newBal, 0, nil
	}

	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET balance_usd = ?, updated_at = ? WHERE id = ?`, newBal, now, workspaceID); err != nil {
		return 0, 0, err
	}
	// `ref` is still written in its legacy "token=<id> model=<name>" form even
	// though both fields now have real columns: /billing/transactions and
	// /workspaces/:id/ledger render it, and it doubles as an audit trail against
	// which the structured columns can be re-derived.
	if _, err := tx.ExecContext(ctx, `INSERT INTO wallet_tx
		(user_id, workspace_id, kind, amount_usd, ref, note, created_at,
		 token_id, model, input_tokens, output_tokens, cache_read_tokens, cache_create_tokens)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, workspaceID, kind, -charged, ref, note, now,
		meta.TokenID, meta.Model, meta.InputTokens, meta.OutputTokens, meta.CacheReadTokens, meta.CacheCreateTokens); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return newBal, charged, nil
}

// PersonalWorkspaceID resolves a user's personal (home) workspace id.
func (db *DB) PersonalWorkspaceID(ctx context.Context, userID int64) (int64, error) {
	var ws int64
	if err := db.QueryRowContext(ctx, `SELECT personal_workspace_id FROM users WHERE id = ?`, userID).Scan(&ws); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if ws == 0 {
		_ = db.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE created_by = ? AND type = 'personal' ORDER BY id LIMIT 1`, userID).Scan(&ws)
	}
	if ws == 0 {
		return 0, ErrNotFound
	}
	return ws, nil
}

// GetWorkspaceBalance returns a workspace's current balance.
func (db *DB) GetWorkspaceBalance(ctx context.Context, workspaceID int64) (float64, error) {
	var bal float64
	err := db.QueryRowContext(ctx, `SELECT balance_usd FROM workspaces WHERE id = ?`, workspaceID).Scan(&bal)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return bal, err
}

// GetBalance returns the user's personal-workspace balance (legacy user-centric
// accessor; unchanged call sites keep working).
func (db *DB) GetBalance(ctx context.Context, userID int64) (float64, error) {
	wsID, err := db.PersonalWorkspaceID(ctx, userID)
	if err != nil {
		return 0, err
	}
	return db.GetWorkspaceBalance(ctx, wsID)
}

// HasEverToppedUp reports whether real money has ever entered this user's
// personal wallet — i.e. at least one `topup` row exists for it.
//
// It answers "has this account ever paid us anything", which is the cheapest
// honest proxy for "is a human behind this account". Bonus credit (`adjust`)
// deliberately does NOT count: on 2026-08-08 a referral farm swept the $1
// signup bonus off its throwaway accounts by having each one issue a gift card
// back to the operator's main account, seconds after registering. Gating gift
// issuance on a topup closes that route without touching the bonus itself —
// farmed credit can still be spent on the account that received it, it just
// cannot be moved to another account.
//
// Both attribution shapes are checked: topups land on the personal workspace
// via addWorkspaceBalanceTx (workspace_id set, user_id = payer), but rows
// written before workspaces existed carry only user_id.
func (db *DB) HasEverToppedUp(ctx context.Context, userID int64) (bool, error) {
	var found int
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM wallet_tx
			 WHERE kind = ?
			   AND (user_id = ?
			     OR workspace_id = (SELECT personal_workspace_id FROM users WHERE id = ?))
		)`, TxKindTopup, userID, userID).Scan(&found)
	if err != nil {
		return false, err
	}
	return found == 1, nil
}

// ListWalletTx returns a page of the user's transactions plus the matching
// total count (for pagination). When `kinds` is non-empty the ledger is
// filtered to those kinds — the billing wallet-history view passes the
// non-charge kinds so the displayed rows and the `total` count stay in lock
// step (per-request charges live in /app/logs instead). Pass nil for the full
// ledger (the admin per-user view).
func (db *DB) ListWalletTx(ctx context.Context, userID int64, kinds []string, limit, offset int) ([]*WalletTx, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	where := `WHERE user_id = ?`
	args := []any{userID}
	if len(kinds) > 0 {
		ph := make([]string, len(kinds))
		for i, k := range kinds {
			ph[i] = "?"
			args = append(args, k)
		}
		where += ` AND kind IN (` + strings.Join(ph, ",") + `)`
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wallet_tx `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `SELECT id, user_id, kind, amount_usd, ref, note, created_at FROM wallet_tx `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*WalletTx
	for rows.Next() {
		var t WalletTx
		var c int64
		if err := rows.Scan(&t.ID, &t.UserID, &t.Kind, &t.AmountUSD, &t.Ref, &t.Note, &c); err != nil {
			return nil, 0, err
		}
		t.CreatedAt = time.Unix(c, 0)
		out = append(out, &t)
	}
	return out, total, rows.Err()
}

// FleetAdjustment is one admin balance adjustment / signup bonus joined to the
// owning user's email, for the operator's fleet-wide adjustments view.
type FleetAdjustment struct {
	ID        int64
	UserID    int64
	Email     string
	AmountUSD float64
	Ref       string
	Note      string
	CreatedAt time.Time
}

// ListFleetAdjustments returns a page of every wallet adjustment across all
// users (kind='adjust' — both manual operator grants and channel signup
// bonuses, told apart by ref), newest first, plus the total count. This is
// the money that entered wallets without a payment order, so it's surfaced
// separately from the alipay_orders payment list.
func (db *DB) ListFleetAdjustments(ctx context.Context, limit, offset int) ([]*FleetAdjustment, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wallet_tx WHERE kind = ?`, TxKindAdjust).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.QueryContext(ctx, `SELECT w.id, w.user_id, COALESCE(u.email, ''), w.amount_usd, w.ref, w.note, w.created_at
		FROM wallet_tx w LEFT JOIN users u ON u.id = w.user_id
		WHERE w.kind = ? ORDER BY w.id DESC LIMIT ? OFFSET ?`, TxKindAdjust, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*FleetAdjustment
	for rows.Next() {
		var a FleetAdjustment
		var c int64
		if err := rows.Scan(&a.ID, &a.UserID, &a.Email, &a.AmountUSD, &a.Ref, &a.Note, &c); err != nil {
			return nil, 0, err
		}
		a.CreatedAt = time.Unix(c, 0)
		out = append(out, &a)
	}
	return out, total, rows.Err()
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

// SumChargeSince returns total absolute USD charged attributed to this user
// since `since` (across whatever workspaces their tokens billed). Used for the
// user's own spend stats.
func (db *DB) SumChargeSince(ctx context.Context, userID int64, since time.Time) (float64, error) {
	var sum sql.NullFloat64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(-amount_usd), 0) FROM wallet_tx WHERE user_id = ? AND kind = 'charge' AND created_at >= ?`,
		userID, since.Unix()).Scan(&sum)
	if err != nil {
		return 0, err
	}
	return sum.Float64, nil
}

// SumChargeSinceForToken returns total USD charged to one API token since
// `since`. userID is included in the predicate both as a tenancy guard and to
// use the (user_id, token_id, created_at) ledger index.
func (db *DB) SumChargeSinceForToken(ctx context.Context, userID, tokenID int64, since time.Time) (float64, error) {
	var sum sql.NullFloat64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(-amount_usd), 0) FROM wallet_tx WHERE user_id = ? AND token_id = ? AND kind = 'charge' AND created_at >= ?`,
		userID, tokenID, since.Unix()).Scan(&sum)
	if err != nil {
		return 0, err
	}
	return sum.Float64, nil
}

// SumChargeSinceForWorkspace returns total USD charged against a workspace's
// pool since `since`. Powers the workspace daily/monthly caps.
func (db *DB) SumChargeSinceForWorkspace(ctx context.Context, workspaceID int64, since time.Time) (float64, error) {
	var sum sql.NullFloat64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(-amount_usd), 0) FROM wallet_tx WHERE workspace_id = ? AND kind = 'charge' AND created_at >= ?`,
		workspaceID, since.Unix()).Scan(&sum)
	if err != nil {
		return 0, err
	}
	return sum.Float64, nil
}

// SumChargeSinceForMember returns total USD a member has charged against a
// specific workspace's pool since `since`. Powers the per-member cap.
func (db *DB) SumChargeSinceForMember(ctx context.Context, workspaceID, userID int64, since time.Time) (float64, error) {
	var sum sql.NullFloat64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(-amount_usd), 0) FROM wallet_tx WHERE workspace_id = ? AND user_id = ? AND kind = 'charge' AND created_at >= ?`,
		workspaceID, userID, since.Unix()).Scan(&sum)
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
			COALESCE((SELECT SUM(balance_usd) FROM workspaces),0)
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
