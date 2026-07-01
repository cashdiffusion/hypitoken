package market

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	// _ "modernc.org/sqlite" registers the SQLite database driver.
	_ "modernc.org/sqlite"
)

// DB is the marketplace's SQLite store. It shares the physical file with the
// shop package (shop.db) but owns its own tables and its own migration-version
// table (market_schema_version), so the two evolve independently. SQLite WAL
// tolerates the two connection pools opened against one file from one process.
type DB struct {
	*sql.DB
}

// Order status values.
const (
	OrderPending   = "pending"   // deposit not yet paid; does NOT reserve a unit
	OrderPaid      = "paid"      // deposit paid (verified notify); reserves a unit
	OrderExpired   = "expired"   // pending too long; unit freed
	OrderCancelled = "cancelled" // operator-cancelled; unit freed
	OrderFulfilled = "fulfilled" // goods handed over (terminal happy path)
)

// Fulfilment methods.
const (
	FulfilPickup   = "pickup"   // 自提 at cfg.PickupLocation
	FulfilDelivery = "delivery" // 配送到楼 — requires dorm address
)

// ErrNotFound is returned by lookups that miss.
var ErrNotFound = errors.New("not found")

// ErrSoldOut — the product's quantity is fully reserved (paid + live pending).
var ErrSoldOut = errors.New("sold out")

// ErrOrderNotPending — the atomic settle path found the order already settled.
var ErrOrderNotPending = errors.New("order not pending")

// migrations is append-only; each entry is one schema delta. Never edit a
// past entry — new tables/columns land at the end.
var migrations = []string{
	// v1 — initial marketplace schema.
	`
CREATE TABLE market_products (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  price REAL NOT NULL,                       -- full price in CNY
  deposit_ratio REAL NOT NULL DEFAULT 0.10,  -- fraction collected at checkout
  quantity INTEGER NOT NULL DEFAULT 1,       -- total units available
  images TEXT NOT NULL DEFAULT '[]',         -- JSON array of image filenames
  active INTEGER NOT NULL DEFAULT 1,
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE market_orders (
  out_trade_no TEXT PRIMARY KEY,
  product_id INTEGER NOT NULL,
  product_name TEXT NOT NULL,
  price REAL NOT NULL,            -- full price snapshot
  deposit_amount REAL NOT NULL,  -- what the buyer actually pays now
  deposit_ratio REAL NOT NULL,   -- ratio snapshot
  status TEXT NOT NULL DEFAULT 'pending',
  pay_method TEXT NOT NULL DEFAULT '',
  trade_no TEXT NOT NULL DEFAULT '',
  pay_url TEXT NOT NULL DEFAULT '',
  qr_code TEXT NOT NULL DEFAULT '',
  fulfil_method TEXT NOT NULL DEFAULT '',  -- pickup | delivery
  contact TEXT NOT NULL DEFAULT '',        -- phone / wechat (buyer notes platform)
  dorm_address TEXT NOT NULL DEFAULT '',   -- required for delivery
  buyer_note TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  paid_at INTEGER NOT NULL DEFAULT 0,
  remote_ip TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_market_orders_product ON market_orders(product_id, status);
CREATE INDEX idx_market_orders_status ON market_orders(status, created_at);
`,
	// v2 — optional fixed deposit amount (CNY). When > 0 it overrides the
	// ratio for this product; 0 keeps the percentage behaviour.
	`ALTER TABLE market_products ADD COLUMN deposit_fixed REAL NOT NULL DEFAULT 0;`,
}

// Open opens (or reuses) the SQLite file at path with WAL + synchronous=FULL
// and applies the marketplace migrations. Same durability story as shop/SaaS.
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
		return nil, fmt.Errorf("market migrate: %w", err)
	}
	return db, nil
}

func (db *DB) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS market_schema_version (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var cur int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM market_schema_version`).Scan(&cur); err != nil {
		return err
	}
	for i := cur; i < len(migrations); i++ {
		if _, err := db.ExecContext(ctx, migrations[i]); err != nil {
			return fmt.Errorf("apply market migration v%d: %w", i+1, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO market_schema_version(version) VALUES(?)`, i+1); err != nil {
			return err
		}
	}
	return nil
}

// --- Products ---

// Product is a marketplace listing. Reserved/Available are derived (not stored)
// and only populated by the list/get helpers that join against live orders.
type Product struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Price        float64   `json:"price"`
	DepositRatio float64   `json:"deposit_ratio"`
	DepositFixed float64   `json:"deposit_fixed"` // when > 0, overrides the ratio
	Quantity     int       `json:"quantity"`
	Images       []string  `json:"images"`
	Active       bool      `json:"active"`
	SortOrder    int       `json:"sort_order"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// Derived:
	Reserved  int `json:"reserved"`  // PAID + fulfilled orders (real money only)
	Available int `json:"available"` // Quantity - Reserved (clamped >= 0)
}

// DepositAmount returns the rounded deposit. A fixed amount (deposit_fixed > 0)
// wins over the ratio; it is capped at the full price. Otherwise it's
// price × ratio.
func (p *Product) DepositAmount() float64 {
	if p.DepositFixed > 0 {
		if p.DepositFixed > p.Price {
			return roundYuan(p.Price)
		}
		return roundYuan(p.DepositFixed)
	}
	return roundYuan(p.Price * p.DepositRatio)
}

// DepositRatioEffective is the deposit as a fraction of price, for display —
// accurate whether the deposit is fixed or ratio-based.
func (p *Product) DepositRatioEffective() float64 {
	if p.Price <= 0 {
		return p.DepositRatio
	}
	return p.DepositAmount() / p.Price
}

// SoldOut reports whether no units remain.
func (p *Product) SoldOut() bool { return p.Available <= 0 }

// PrimaryImage returns the first image filename, or "" if none.
func (p *Product) PrimaryImage() string {
	if len(p.Images) == 0 {
		return ""
	}
	return p.Images[0]
}

const productCols = `id, name, description, price, deposit_ratio, deposit_fixed, quantity, images, active, sort_order, created_at, updated_at`

func scanProduct(row interface{ Scan(...any) error }) (*Product, error) {
	var p Product
	var act int
	var imagesJSON string
	var c, u int64
	if err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.DepositRatio, &p.DepositFixed, &p.Quantity, &imagesJSON, &act, &p.SortOrder, &c, &u); err != nil {
		return nil, err
	}
	p.Active = act == 1
	p.CreatedAt = time.Unix(c, 0)
	p.UpdatedAt = time.Unix(u, 0)
	if strings.TrimSpace(imagesJSON) != "" {
		_ = json.Unmarshal([]byte(imagesJSON), &p.Images)
	}
	if p.Images == nil {
		p.Images = []string{}
	}
	return &p, nil
}

// reservedCounts returns product_id → count of units locked by orders whose
// deposit is ACTUALLY PAID (paid, fulfilled). A product only sells out once real
// money has arrived (a signature-verified Z-Pay notify → MarkPaid). Pending
// (unpaid) orders never reserve a unit, so an abandoned checkout can't make an
// item look sold out; expired/cancelled orders free their unit.
func (db *DB) reservedCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT product_id, COUNT(*) FROM market_orders WHERE status IN (?,?) GROUP BY product_id`,
		OrderPaid, OrderFulfilled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var pid int64
		var n int
		if err := rows.Scan(&pid, &n); err != nil {
			return nil, err
		}
		out[pid] = n
	}
	return out, rows.Err()
}

// fillDerived populates Reserved/Available on each product from a counts map.
func fillDerived(products []*Product, reserved map[int64]int) {
	for _, p := range products {
		p.Reserved = reserved[p.ID]
		p.Available = max(p.Quantity-p.Reserved, 0)
	}
}

// CreateProduct inserts a new listing and back-fills its ID.
func (db *DB) CreateProduct(ctx context.Context, p *Product) error {
	now := time.Now().Unix()
	imagesJSON := marshalImages(p.Images)
	act := 0
	if p.Active {
		act = 1
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO market_products (name, description, price, deposit_ratio, deposit_fixed, quantity, images, active, sort_order, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.Description, p.Price, p.DepositRatio, p.DepositFixed, p.Quantity, imagesJSON, act, p.SortOrder, now, now)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	p.CreatedAt = time.Unix(now, 0)
	p.UpdatedAt = p.CreatedAt
	return nil
}

// GetProduct returns one listing with derived availability.
func (db *DB) GetProduct(ctx context.Context, id int64) (*Product, error) {
	row := db.QueryRowContext(ctx, `SELECT `+productCols+` FROM market_products WHERE id = ?`, id)
	p, err := scanProduct(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	reserved, err := db.reservedCounts(ctx)
	if err != nil {
		return nil, err
	}
	fillDerived([]*Product{p}, reserved)
	return p, nil
}

// ListProducts returns listings ordered by sort_order then newest. When
// activeOnly, hidden products are excluded.
func (db *DB) ListProducts(ctx context.Context, activeOnly bool) ([]*Product, error) {
	q := `SELECT ` + productCols + ` FROM market_products`
	if activeOnly {
		q += ` WHERE active = 1`
	}
	q += ` ORDER BY sort_order ASC, id DESC`
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	reserved, err := db.reservedCounts(ctx)
	if err != nil {
		return nil, err
	}
	fillDerived(out, reserved)
	return out, nil
}

// UpdateProduct writes the mutable fields of an existing listing.
func (db *DB) UpdateProduct(ctx context.Context, p *Product) error {
	now := time.Now().Unix()
	act := 0
	if p.Active {
		act = 1
	}
	_, err := db.ExecContext(ctx,
		`UPDATE market_products SET name=?, description=?, price=?, deposit_ratio=?, deposit_fixed=?, quantity=?, images=?, active=?, sort_order=?, updated_at=? WHERE id=?`,
		p.Name, p.Description, p.Price, p.DepositRatio, p.DepositFixed, p.Quantity, marshalImages(p.Images), act, p.SortOrder, now, p.ID)
	return err
}

// SetImages overwrites just the images column (used by the upload/delete
// handlers without re-reading the whole product).
func (db *DB) SetImages(ctx context.Context, id int64, images []string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE market_products SET images=?, updated_at=? WHERE id=?`,
		marshalImages(images), time.Now().Unix(), id)
	return err
}

// DeleteProduct removes a listing. Orders keep their product_name snapshot, so
// historical orders stay readable after deletion.
func (db *DB) DeleteProduct(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM market_products WHERE id = ?`, id)
	return err
}

// --- Orders ---

type Order struct {
	OutTradeNo    string    `json:"out_trade_no"`
	ProductID     int64     `json:"product_id"`
	ProductName   string    `json:"product_name"`
	Price         float64   `json:"price"`
	DepositAmount float64   `json:"deposit_amount"`
	DepositRatio  float64   `json:"deposit_ratio"`
	Status        string    `json:"status"`
	PayMethod     string    `json:"pay_method"`
	TradeNo       string    `json:"trade_no"`
	PayURL        string    `json:"pay_url"`
	QRCode        string    `json:"qr_code"`
	FulfilMethod  string    `json:"fulfil_method"`
	Contact       string    `json:"contact"`
	DormAddress   string    `json:"dorm_address"`
	BuyerNote     string    `json:"buyer_note"`
	CreatedAt     time.Time `json:"created_at"`
	PaidAt        time.Time `json:"paid_at"`
	RemoteIP      string    `json:"remote_ip"`
}

// Balance returns the outstanding amount due on pickup/delivery (price minus
// the deposit already paid).
func (o *Order) Balance() float64 { return roundYuan(o.Price - o.DepositAmount) }

const orderCols = `out_trade_no, product_id, product_name, price, deposit_amount, deposit_ratio, status, pay_method, trade_no, pay_url, qr_code, fulfil_method, contact, dorm_address, buyer_note, created_at, paid_at, remote_ip`

func scanOrder(row interface{ Scan(...any) error }) (*Order, error) {
	var o Order
	var created, paid int64
	if err := row.Scan(&o.OutTradeNo, &o.ProductID, &o.ProductName, &o.Price, &o.DepositAmount, &o.DepositRatio,
		&o.Status, &o.PayMethod, &o.TradeNo, &o.PayURL, &o.QRCode, &o.FulfilMethod, &o.Contact, &o.DormAddress,
		&o.BuyerNote, &created, &paid, &o.RemoteIP); err != nil {
		return nil, err
	}
	o.CreatedAt = time.Unix(created, 0)
	if paid > 0 {
		o.PaidAt = time.Unix(paid, 0)
	}
	return &o, nil
}

// CreateOrderReserving atomically checks the product still has a unit that
// hasn't been PAID for (quantity > paid+fulfilled orders) and, if so, inserts
// the pending order. Pending orders don't hold inventory — sold-out is decided
// by real, paid deposits only — so the product only rejects new checkouts once
// `quantity` units have actually been paid. Returns ErrSoldOut when full.
func (db *DB) CreateOrderReserving(ctx context.Context, o *Order) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	var quantity int
	err = tx.QueryRowContext(ctx, `SELECT quantity FROM market_products WHERE id = ? AND active = 1`, o.ProductID).Scan(&quantity)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	var reserved int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM market_orders WHERE product_id = ? AND status IN (?,?)`,
		o.ProductID, OrderPaid, OrderFulfilled).Scan(&reserved); err != nil {
		return err
	}
	if reserved >= quantity {
		return ErrSoldOut
	}

	now := time.Now().Unix()
	o.CreatedAt = time.Unix(now, 0)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO market_orders (`+orderCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.OutTradeNo, o.ProductID, o.ProductName, o.Price, o.DepositAmount, o.DepositRatio,
		o.Status, o.PayMethod, o.TradeNo, o.PayURL, o.QRCode, o.FulfilMethod, o.Contact, o.DormAddress,
		o.BuyerNote, now, 0, o.RemoteIP); err != nil {
		return err
	}
	return tx.Commit()
}

// SetPayInfo stores the Z-Pay payment surface (pay_url/qr_code/method) after
// the gateway mints the order.
func (db *DB) SetPayInfo(ctx context.Context, outTradeNo, payURL, qrCode, method string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE market_orders SET pay_url=?, qr_code=?, pay_method=? WHERE out_trade_no=?`,
		payURL, qrCode, method, outTradeNo)
	return err
}

// GetOrder returns one order by its id.
func (db *DB) GetOrder(ctx context.Context, outTradeNo string) (*Order, error) {
	row := db.QueryRowContext(ctx, `SELECT `+orderCols+` FROM market_orders WHERE out_trade_no = ?`, outTradeNo)
	o, err := scanOrder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

// MarkPaid atomically flips a pending (or expired — async Alipay can confirm
// after the sweeper fired) order to paid, recording the upstream trade_no.
// Idempotent: an already-paid order returns ErrOrderNotPending.
func (db *DB) MarkPaid(ctx context.Context, outTradeNo, tradeNo string) (*Order, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	row := tx.QueryRowContext(ctx, `SELECT `+orderCols+` FROM market_orders WHERE out_trade_no = ?`, outTradeNo)
	o, err := scanOrder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if o.Status != OrderPending && o.Status != OrderExpired {
		return nil, ErrOrderNotPending
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE market_orders SET status=?, trade_no=?, paid_at=? WHERE out_trade_no=?`,
		OrderPaid, tradeNo, now, outTradeNo); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	o.Status = OrderPaid
	o.TradeNo = tradeNo
	o.PaidAt = time.Unix(now, 0)
	return o, nil
}

// SetStatus moves an order to a terminal operator state (fulfilled/cancelled).
func (db *DB) SetStatus(ctx context.Context, outTradeNo, status string) error {
	_, err := db.ExecContext(ctx, `UPDATE market_orders SET status=? WHERE out_trade_no=?`, status, outTradeNo)
	return err
}

// ExpirePendingBefore marks pending orders older than cutoff as expired,
// freeing their reserved units. Returns the number flipped.
func (db *DB) ExpirePendingBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE market_orders SET status=? WHERE status=? AND created_at < ?`,
		OrderExpired, OrderPending, cutoff.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListOrders returns orders, newest first, optionally filtered by status.
func (db *DB) ListOrders(ctx context.Context, statusFilter string, limit int) ([]*Order, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `SELECT ` + orderCols + ` FROM market_orders`
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

// --- helpers ---

func marshalImages(images []string) string {
	if images == nil {
		images = []string{}
	}
	b, err := json.Marshal(images)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// roundYuan rounds to 2 decimals (cents) — Z-Pay's money field is %.2f.
func roundYuan(v float64) float64 {
	return math.Round(v*100) / 100
}
