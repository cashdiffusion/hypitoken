package shop

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB is the shop's private SQLite store. Independent of the SaaS DB so
// the shop can run with SaaS off and so its schema evolution doesn't
// have to coordinate with the wallet system.
type DB struct {
	*sql.DB
}

// Order status values.
const (
	OrderPending     = "pending"
	OrderPaid        = "paid"
	OrderExpired     = "expired"
	OrderAwaitManual = "await_manual" // paid but no stock — operator must fulfil
)

// Delivery types.
const (
	DeliveryAuto = "auto" // delivery_template returned verbatim
	DeliveryCard = "card" // pop one row from shop_card_secrets
)

// ErrNotFound is returned by lookups that miss.
var ErrNotFound = errors.New("not found")

// ErrOrderNotPending is returned by the atomic mark-paid path when a
// concurrent webhook already settled the order. Caller treats as no-op.
var ErrOrderNotPending = errors.New("order not pending")

// ErrOutOfStock — payment confirmed but card pool drained. Order moves
// to await_manual and operator is notified via the admin order list.
var ErrOutOfStock = errors.New("out of stock")

// migrations is append-only; each entry is one schema delta. New tables
// land at the end — never edit a past entry.
var migrations = []string{
	// v1 — initial shop schema.
	`
CREATE TABLE shop_products (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  price_cny REAL NOT NULL,
  delivery_type TEXT NOT NULL DEFAULT 'auto',
  delivery_template TEXT NOT NULL DEFAULT '',
  stock_available INTEGER NOT NULL DEFAULT 0,
  active INTEGER NOT NULL DEFAULT 1,
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE shop_card_secrets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  product_id INTEGER NOT NULL REFERENCES shop_products(id) ON DELETE CASCADE,
  content TEXT NOT NULL,
  consumed INTEGER NOT NULL DEFAULT 0,
  consumed_by_order TEXT NOT NULL DEFAULT '',
  consumed_at INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_card_secrets_avail ON shop_card_secrets(product_id, consumed, id);

CREATE TABLE shop_orders (
  out_trade_no TEXT PRIMARY KEY,
  product_id INTEGER NOT NULL,
  product_name TEXT NOT NULL,
  email TEXT NOT NULL,
  query_pass_hash TEXT NOT NULL,
  amount_cny REAL NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  pay_method TEXT NOT NULL DEFAULT '',
  trade_no TEXT NOT NULL DEFAULT '',
  pay_url TEXT NOT NULL DEFAULT '',
  qr_code TEXT NOT NULL DEFAULT '',
  fulfillment TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  paid_at INTEGER NOT NULL DEFAULT 0,
  email_sent INTEGER NOT NULL DEFAULT 0,
  remote_ip TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_shop_orders_email ON shop_orders(email);
CREATE INDEX idx_shop_orders_status ON shop_orders(status, created_at);
`,
}

// Open opens the shop SQLite at path with WAL + synchronous=FULL and
// applies migrations. Same durability story as the SaaS DB.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)", path)
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sdb.SetMaxOpenConns(8)
	sdb.SetMaxIdleConns(4)
	if err := sdb.Ping(); err != nil {
		return nil, err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(path+suffix, 0o600)
	}
	db := &DB{DB: sdb}
	if err := db.migrate(); err != nil {
		_ = sdb.Close()
		return nil, fmt.Errorf("shop migrate: %w", err)
	}
	return db, nil
}

func (db *DB) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS shop_schema_version (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var cur int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM shop_schema_version`).Scan(&cur); err != nil {
		return err
	}
	for i, mig := range migrations {
		v := i + 1
		if v <= cur {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, mig); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration v%d: %w", v, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO shop_schema_version(version) VALUES(?)`, v); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// --- Product CRUD ---

type Product struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	PriceCNY         float64   `json:"price_cny"`
	DeliveryType     string    `json:"delivery_type"`
	DeliveryTemplate string    `json:"delivery_template"`
	StockAvailable   int       `json:"stock_available"`
	Active           bool      `json:"active"`
	SortOrder        int       `json:"sort_order"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

const productCols = `id, name, description, price_cny, delivery_type, delivery_template, stock_available, active, sort_order, created_at, updated_at`

func scanProduct(row interface{ Scan(...any) error }) (*Product, error) {
	var p Product
	var act int
	var c, u int64
	if err := row.Scan(&p.ID, &p.Name, &p.Description, &p.PriceCNY, &p.DeliveryType, &p.DeliveryTemplate, &p.StockAvailable, &act, &p.SortOrder, &c, &u); err != nil {
		return nil, err
	}
	p.Active = act == 1
	p.CreatedAt = time.Unix(c, 0)
	p.UpdatedAt = time.Unix(u, 0)
	return &p, nil
}

// CreateProduct inserts a new row. Stock is computed at delivery time so
// callers can pass StockAvailable=0; AppendCardSecrets bumps it.
func (db *DB) CreateProduct(ctx context.Context, p *Product) error {
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx, `INSERT INTO shop_products
		(name, description, price_cny, delivery_type, delivery_template, stock_available, active, sort_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		p.Name, p.Description, p.PriceCNY, p.DeliveryType, p.DeliveryTemplate,
		boolToInt(p.Active), p.SortOrder, now, now)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	p.CreatedAt = time.Unix(now, 0)
	p.UpdatedAt = p.CreatedAt
	return nil
}

func (db *DB) GetProduct(ctx context.Context, id int64) (*Product, error) {
	row := db.QueryRowContext(ctx, `SELECT `+productCols+` FROM shop_products WHERE id = ?`, id)
	p, err := scanProduct(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListProducts returns every row; pass activeOnly=true for the storefront,
// false for the admin panel.
func (db *DB) ListProducts(ctx context.Context, activeOnly bool) ([]*Product, error) {
	q := `SELECT ` + productCols + ` FROM shop_products`
	if activeOnly {
		q += ` WHERE active = 1`
	}
	q += ` ORDER BY sort_order ASC, id ASC`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateProduct rewrites the editable fields. ID, created_at, stock_available
// are untouched (stock is managed via Append/RemoveCardSecrets).
func (db *DB) UpdateProduct(ctx context.Context, p *Product) error {
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx, `UPDATE shop_products SET
		name = ?, description = ?, price_cny = ?, delivery_type = ?,
		delivery_template = ?, active = ?, sort_order = ?, updated_at = ?
		WHERE id = ?`,
		p.Name, p.Description, p.PriceCNY, p.DeliveryType, p.DeliveryTemplate,
		boolToInt(p.Active), p.SortOrder, now, p.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	p.UpdatedAt = time.Unix(now, 0)
	return nil
}

func (db *DB) DeleteProduct(ctx context.Context, id int64) error {
	res, err := db.ExecContext(ctx, `DELETE FROM shop_products WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Card-secret pool ---

// AppendCardSecrets bulk-inserts lines (one per row), updates stock_available,
// and returns the count actually inserted. Empty lines are skipped.
func (db *DB) AppendCardSecrets(ctx context.Context, productID int64, lines []string) (int, error) {
	clean := make([]string, 0, len(lines))
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t != "" {
			clean = append(clean, t)
		}
	}
	if len(clean) == 0 {
		return 0, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO shop_card_secrets (product_id, content, created_at) VALUES (?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for _, c := range clean {
		if _, err := stmt.ExecContext(ctx, productID, c, now); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE shop_products SET stock_available = stock_available + ?, updated_at = ? WHERE id = ?`,
		len(clean), now, productID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(clean), nil
}

// CardSecret is the admin-view shape — we don't expose `content` of unsold
// cards via the public API. Admin sees both.
type CardSecret struct {
	ID              int64     `json:"id"`
	ProductID       int64     `json:"product_id"`
	Content         string    `json:"content"`
	Consumed        bool      `json:"consumed"`
	ConsumedByOrder string    `json:"consumed_by_order,omitempty"`
	ConsumedAt      time.Time `json:"consumed_at,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

func (db *DB) ListCardSecrets(ctx context.Context, productID int64, includeConsumed bool, limit int) ([]*CardSecret, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT id, product_id, content, consumed, consumed_by_order, consumed_at, created_at FROM shop_card_secrets WHERE product_id = ?`
	if !includeConsumed {
		q += ` AND consumed = 0`
	}
	q += ` ORDER BY id ASC LIMIT ?`
	rows, err := db.QueryContext(ctx, q, productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CardSecret
	for rows.Next() {
		var cs CardSecret
		var consumed int
		var cAt, crAt int64
		if err := rows.Scan(&cs.ID, &cs.ProductID, &cs.Content, &consumed, &cs.ConsumedByOrder, &cAt, &crAt); err != nil {
			return nil, err
		}
		cs.Consumed = consumed == 1
		if cAt > 0 {
			cs.ConsumedAt = time.Unix(cAt, 0)
		}
		cs.CreatedAt = time.Unix(crAt, 0)
		out = append(out, &cs)
	}
	return out, rows.Err()
}

// DeleteUnconsumedSecret deletes one unsold card by id and decrements stock.
// Refuses to touch a consumed row (would corrupt order fulfillment).
func (db *DB) DeleteUnconsumedSecret(ctx context.Context, id int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var productID int64
	var consumed int
	err = tx.QueryRowContext(ctx, `SELECT product_id, consumed FROM shop_card_secrets WHERE id = ?`, id).Scan(&productID, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if consumed == 1 {
		return errors.New("card already consumed")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM shop_card_secrets WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE shop_products SET stock_available = MAX(0, stock_available - 1), updated_at = ? WHERE id = ?`,
		time.Now().Unix(), productID); err != nil {
		return err
	}
	return tx.Commit()
}

// --- Orders ---

type Order struct {
	OutTradeNo    string    `json:"out_trade_no"`
	ProductID     int64     `json:"product_id"`
	ProductName   string    `json:"product_name"`
	Email         string    `json:"email"`
	QueryPassHash string    `json:"-"` // never expose
	AmountCNY     float64   `json:"amount_cny"`
	Status        string    `json:"status"`
	PayMethod     string    `json:"pay_method"`
	TradeNo       string    `json:"trade_no,omitempty"`
	PayURL        string    `json:"pay_url,omitempty"`
	QRCode        string    `json:"qr_code,omitempty"`
	Fulfillment   string    `json:"fulfillment,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	PaidAt        time.Time `json:"paid_at,omitempty"`
	EmailSent     bool      `json:"email_sent"`
	RemoteIP      string    `json:"remote_ip,omitempty"`
}

const orderCols = `out_trade_no, product_id, product_name, email, query_pass_hash, amount_cny, status, pay_method, trade_no, pay_url, qr_code, fulfillment, created_at, paid_at, email_sent, remote_ip`

func scanOrder(row interface{ Scan(...any) error }) (*Order, error) {
	var o Order
	var c, p int64
	var emailSent int
	if err := row.Scan(&o.OutTradeNo, &o.ProductID, &o.ProductName, &o.Email, &o.QueryPassHash,
		&o.AmountCNY, &o.Status, &o.PayMethod, &o.TradeNo, &o.PayURL, &o.QRCode, &o.Fulfillment,
		&c, &p, &emailSent, &o.RemoteIP); err != nil {
		return nil, err
	}
	o.EmailSent = emailSent == 1
	o.CreatedAt = time.Unix(c, 0)
	if p > 0 {
		o.PaidAt = time.Unix(p, 0)
	}
	return &o, nil
}

func (db *DB) CreateOrder(ctx context.Context, o *Order) error {
	now := time.Now().Unix()
	_, err := db.ExecContext(ctx, `INSERT INTO shop_orders
		(out_trade_no, product_id, product_name, email, query_pass_hash, amount_cny,
		 status, pay_method, trade_no, pay_url, qr_code, fulfillment, created_at, paid_at, email_sent, remote_ip)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, '', ?, 0, 0, ?)`,
		o.OutTradeNo, o.ProductID, o.ProductName, o.Email, o.QueryPassHash, o.AmountCNY,
		OrderPending, o.PayMethod, o.PayURL, o.QRCode, now, o.RemoteIP)
	if err != nil {
		return err
	}
	o.Status = OrderPending
	o.CreatedAt = time.Unix(now, 0)
	return nil
}

func (db *DB) GetOrder(ctx context.Context, outTradeNo string) (*Order, error) {
	row := db.QueryRowContext(ctx, `SELECT `+orderCols+` FROM shop_orders WHERE out_trade_no = ?`, outTradeNo)
	o, err := scanOrder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

// MarkOrderPaidAndFulfil is the atomic "I got the Z-Pay notify" path.
// Inside a single transaction:
//  1. flip status pending → paid (or stay no-op if already settled)
//  2. for delivery_type=card: pop one unconsumed secret, mark it consumed
//     by this order; if pool empty, mark status=await_manual and let
//     caller email a "we'll deliver shortly" message
//  3. for delivery_type=auto: copy product.delivery_template
//  4. write fulfilment text + trade_no + paid_at
//
// Returns the order + the fulfilment string the caller should email.
// On a duplicate webhook (status != pending), returns ErrOrderNotPending
// and leaves DB unchanged.
func (db *DB) MarkOrderPaidAndFulfil(ctx context.Context, outTradeNo, tradeNo string) (*Order, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Lock-by-condition: only proceed if still pending.
	o, err := lockPendingOrder(ctx, tx, outTradeNo)
	if err != nil {
		return nil, err
	}

	// Look up the product's delivery config.
	var delType, delTemplate string
	err = tx.QueryRowContext(ctx, `SELECT delivery_type, delivery_template FROM shop_products WHERE id = ?`, o.ProductID).Scan(&delType, &delTemplate)
	if errors.Is(err, sql.ErrNoRows) {
		// Product was deleted between order creation and payment.
		// Fall back to await_manual so the operator can refund or hand-deliver.
		delType = "await"
	} else if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	newStatus := OrderPaid
	fulfilment := ""

	switch delType {
	case DeliveryAuto:
		fulfilment = delTemplate
	case DeliveryCard:
		// Pop the lowest-id unconsumed card for this product.
		var cardID int64
		var content string
		err = tx.QueryRowContext(ctx,
			`SELECT id, content FROM shop_card_secrets WHERE product_id = ? AND consumed = 0 ORDER BY id ASC LIMIT 1`,
			o.ProductID).Scan(&cardID, &content)
		if errors.Is(err, sql.ErrNoRows) {
			// Drained. Mark order as await_manual so the operator sees it
			// in the admin queue. The buyer still gets an email letting
			// them know payment landed but delivery is pending.
			newStatus = OrderAwaitManual
		} else if err != nil {
			return nil, err
		} else {
			if _, err := tx.ExecContext(ctx,
				`UPDATE shop_card_secrets SET consumed = 1, consumed_by_order = ?, consumed_at = ? WHERE id = ?`,
				outTradeNo, now, cardID); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE shop_products SET stock_available = MAX(0, stock_available - 1), updated_at = ? WHERE id = ?`,
				now, o.ProductID); err != nil {
				return nil, err
			}
			fulfilment = content
		}
	default:
		newStatus = OrderAwaitManual
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE shop_orders SET status = ?, trade_no = ?, fulfillment = ?, paid_at = ? WHERE out_trade_no = ?`,
		newStatus, tradeNo, fulfilment, now, outTradeNo); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	o.Status = newStatus
	o.TradeNo = tradeNo
	o.Fulfillment = fulfilment
	o.PaidAt = time.Unix(now, 0)
	return o, nil
}

// lockPendingOrder reads the row with an UPDATE-style guard: if status
// isn't pending, return ErrOrderNotPending without holding any row lock.
// SQLite serializes writers so this is safe enough — the trailing UPDATE
// in the caller transaction is the real consistency anchor.
func lockPendingOrder(ctx context.Context, tx *sql.Tx, outTradeNo string) (*Order, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+orderCols+` FROM shop_orders WHERE out_trade_no = ?`, outTradeNo)
	o, err := scanOrder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if o.Status != OrderPending {
		return nil, ErrOrderNotPending
	}
	return o, nil
}

// MarkEmailSent flips the email_sent flag. Called by the notify handler
// once the SMTP send returns nil; admin can re-send manually if false.
func (db *DB) MarkEmailSent(ctx context.Context, outTradeNo string) error {
	_, err := db.ExecContext(ctx, `UPDATE shop_orders SET email_sent = 1 WHERE out_trade_no = ?`, outTradeNo)
	return err
}

// SetFulfillment lets the admin manually inject a delivery message for an
// await_manual order, flipping it to paid in the same step.
func (db *DB) SetFulfillment(ctx context.Context, outTradeNo, fulfillment string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE shop_orders SET fulfillment = ?, status = ? WHERE out_trade_no = ? AND status IN (?, ?)`,
		fulfillment, OrderPaid, outTradeNo, OrderAwaitManual, OrderPaid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ExpirePendingBefore atomically transitions every pending order older
// than cutoff to expired. Returns the count touched.
func (db *DB) ExpirePendingBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE shop_orders SET status = ? WHERE status = ? AND created_at < ?`,
		OrderExpired, OrderPending, cutoff.Unix())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ListOrders is the admin order list. statusFilter="" returns everything.
func (db *DB) ListOrders(ctx context.Context, statusFilter string, limit int) ([]*Order, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT ` + orderCols + ` FROM shop_orders`
	var args []any
	if statusFilter != "" {
		q += ` WHERE status = ?`
		args = append(args, statusFilter)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
