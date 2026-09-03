package db

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"
)

// Idempotent wallet movement, for billers that live in another process.
//
// Every caller of ChargeWorkspaceWithFloor so far has been in-process and
// one-shot: the proxy settles a turn exactly once and cannot retry, so a
// duplicate charge was structurally impossible and the ledger needed no replay
// guard. The sibling HypiHub service bills across HTTP, where the caller
// genuinely cannot distinguish "the charge did not happen" from "the charge
// happened and the response was lost" — so it must be allowed to retry, and a
// retry must be free.
//
// The guard is the v22 partial UNIQUE index on wallet_tx(idem_key), NOT the
// SELECT below. Two concurrent retries can both find nothing; only one of them
// can land the INSERT. The loser reads the winner's row back and reports it as
// a replay, which is why a constraint violation here is an expected outcome
// rather than an error to propagate.

// ErrIdemConflict is returned when an idempotency key is presented again for a
// materially different movement — a different workspace, a different attributed
// user, or a refund key reused for a charge. Replaying a key must return the
// ORIGINAL row; silently honouring a mismatched replay would let a bug (or a
// key-generation collision) move money between wallets.
var ErrIdemConflict = errors.New("idempotency key already used for a different movement")

// maxIdemAmountUSD is a structural sanity ceiling on a single service-billed
// movement. It is not a business limit — it exists so a unit slip upstream
// (cents billed as dollars, a NaN cast, a runaway loop) cannot drain a wallet
// in one call. HTTP callers should reject well below this.
const maxIdemAmountUSD = 1000.0

// MaxIdemAmountUSD is maxIdemAmountUSD for callers outside this package, so
// an in-process biller can tell when a movement is too large for the
// idempotent path and must fall back to the unbounded one.
const MaxIdemAmountUSD = maxIdemAmountUSD

// IdemChargeReq is one idempotent wallet movement. AmountUSD is always POSITIVE
// and states the magnitude; the direction comes from which function is called
// (ChargeWorkspaceIdem debits, CreditWorkspaceIdem credits).
type IdemChargeReq struct {
	// IdempotencyKey uniquely names this movement across retries. Required,
	// and unique across the whole ledger — namespace it by product and job
	// (e.g. "hypihub:job:job_7f3a" / "hypihub:job:job_7f3a:refund").
	IdempotencyKey string

	// Product names the biller, recorded on the row so per-product revenue is
	// answerable from the ledger alone. "" means the proxy itself.
	Product string

	// WorkspaceID is the billing subject (whose pool moves). UserID is the
	// member attributed with the movement. Both are recorded, exactly as on
	// the in-process charge path.
	WorkspaceID int64
	UserID      int64

	AmountUSD float64
	Ref       string
	Note      string

	// MaxOverdraftUSD bounds how far a debit may drive the wallet negative;
	// the charge is clamped to rest exactly on -MaxOverdraftUSD. Ignored by
	// CreditWorkspaceIdem. <= 0 disables the floor.
	MaxOverdraftUSD float64

	Meta ChargeMeta
}

// IdemChargeResult reports what the ledger actually did.
type IdemChargeResult struct {
	// TxID is the wallet_tx row. 0 when a debit clamped to nothing (see
	// ChargeWorkspaceIdem) — no row is written for a zero movement.
	TxID int64
	// ChargedUSD is the magnitude actually moved, which is what the caller
	// must record: a clamped charge bills less than it asked for.
	ChargedUSD float64
	// NewBalanceUSD is the workspace balance after the movement (or the live
	// balance on a replay).
	NewBalanceUSD float64
	// Clamped is true when the overdraft floor cut the debit short.
	Clamped bool
	// Replayed is true when this key had already been settled and no balance
	// was touched by this call.
	Replayed bool
}

// ChargeWorkspaceIdem debits a workspace exactly once per idempotency key.
//
// First call: identical in every respect to ChargeWorkspaceWithFloor — same
// overdraft clamp (clampCharge), same NEGATIVE amount_usd convention, same
// ChargeMeta columns — plus idem_key and product stamped on the row.
//
// Later calls with the same key: the original row is returned with
// Replayed=true and the balance is not touched.
//
// One deliberate gap: when the clamp takes the charge down to zero (the wallet
// is already at the overdraft floor) no row is written, so that key stays
// unclaimed and a later retry can still bill it. That mirrors
// ChargeWorkspaceWithFloor, and it is the behaviour we want — nothing was
// billed, so nothing is being re-billed, and if the customer tops up in the
// meantime the money they genuinely owe can still be collected.
func (db *DB) ChargeWorkspaceIdem(ctx context.Context, req IdemChargeReq) (IdemChargeResult, error) {
	return db.walletIdem(ctx, req, TxKindCharge)
}

// CreditWorkspaceIdem credits a workspace exactly once per idempotency key,
// writing a POSITIVE kind='refund' row. The mirror of ChargeWorkspaceIdem:
// same replay rules, no overdraft clamp (a credit cannot breach a floor), so
// Clamped is always false.
//
// Refunds are unbounded upward on purpose — the caller is expected to refund at
// most what it charged, and this layer cannot verify that without coupling to
// the caller's job model. The HTTP layer validates the amount against its
// ceiling before calling.
func (db *DB) CreditWorkspaceIdem(ctx context.Context, req IdemChargeReq) (IdemChargeResult, error) {
	return db.walletIdem(ctx, req, TxKindRefund)
}

func (db *DB) walletIdem(ctx context.Context, req IdemChargeReq, kind string) (res IdemChargeResult, err error) {
	// Same corruption escalation as the in-process charge path: a damaged file
	// shows up first on the busiest write path, and on 2026-08-18 nothing
	// escalated it. See integrity.go.
	defer func() { db.Report(err) }()

	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" {
		return IdemChargeResult{}, errors.New("idempotency key required")
	}
	if req.WorkspaceID <= 0 {
		return IdemChargeResult{}, errors.New("workspace id required")
	}
	if math.IsNaN(req.AmountUSD) || math.IsInf(req.AmountUSD, 0) || req.AmountUSD <= 0 {
		return IdemChargeResult{}, errors.New("amount_usd must be positive")
	}
	if req.AmountUSD > maxIdemAmountUSD {
		return IdemChargeResult{}, errors.New("amount_usd exceeds the per-call ceiling")
	}

	res, err = db.walletIdemOnce(ctx, req, kind)
	if err == nil || !isUniqueViolation(err) {
		return res, err
	}
	// Lost the insert race: a concurrent caller committed this key between our
	// SELECT and our INSERT. Its row is the authoritative one — read it back
	// and report the replay, rather than surfacing a constraint error the
	// caller would (correctly) retry forever.
	replay, found, rerr := findIdemRow(ctx, db, req, kind)
	if rerr != nil {
		return IdemChargeResult{}, rerr
	}
	if !found {
		// The key exists (we violated the index) but is not visible to us. Not
		// reachable in practice; returning the original error beats inventing
		// a result for a movement we cannot describe.
		return IdemChargeResult{}, err
	}
	return replay, nil
}

func (db *DB) walletIdemOnce(ctx context.Context, req IdemChargeReq, kind string) (IdemChargeResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return IdemChargeResult{}, err
	}
	defer tx.Rollback() //nolint:errcheck // rolled back only if Commit didn't run

	// Fast path for the common retry: the key is already settled.
	if replay, found, ferr := findIdemRow(ctx, tx, req, kind); ferr != nil {
		return IdemChargeResult{}, ferr
	} else if found {
		return replay, nil
	}

	var bal float64
	if err := tx.QueryRowContext(ctx, `SELECT balance_usd FROM workspaces WHERE id = ?`, req.WorkspaceID).Scan(&bal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IdemChargeResult{}, ErrNotFound
		}
		return IdemChargeResult{}, err
	}

	var moved, delta float64
	clamped := false
	if kind == TxKindCharge {
		moved = clampCharge(bal, req.AmountUSD, req.MaxOverdraftUSD)
		clamped = moved < req.AmountUSD
		delta = -moved
	} else {
		moved = req.AmountUSD
		delta = moved
	}
	newBal := bal + delta
	if moved == 0 {
		// Wallet already resting on the overdraft floor. Nothing moved, so
		// nothing is recorded and the key stays unclaimed.
		return IdemChargeResult{NewBalanceUSD: newBal, Clamped: clamped}, nil
	}

	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET balance_usd = ?, updated_at = ? WHERE id = ?`, newBal, now, req.WorkspaceID); err != nil {
		return IdemChargeResult{}, err
	}
	txID, err := insertLedgerTx(ctx, tx, ledgerRow{
		UserID:      req.UserID,
		WorkspaceID: req.WorkspaceID,
		Kind:        kind,
		AmountUSD:   delta,
		Ref:         req.Ref,
		Note:        req.Note,
		CreatedAt:   now,
		Meta:        req.Meta,
		IdemKey:     req.IdempotencyKey,
		Product:     req.Product,
	})
	if err != nil {
		return IdemChargeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return IdemChargeResult{}, err
	}
	return IdemChargeResult{TxID: txID, ChargedUSD: moved, NewBalanceUSD: newBal, Clamped: clamped}, nil
}

// The two idempotency lookups, shared with the query-plan test.
//
// Both carry a redundant-looking `AND idem_key <> ”`. It is not redundant.
// idx_wallet_tx_idem is PARTIAL (`WHERE idem_key <> ”`, see migration v22), and
// SQLite only uses a partial index when the query's WHERE clause contains the
// index's own predicate — it will infer `x IS NOT NULL` from `x = ?`, but it
// will not infer `x <> ”`. Without the guard, `WHERE idem_key = ?` planned as
// SCAN wallet_tx: 1.39M rows, ~0.4s, and walletIdemOnce runs it INSIDE the
// BEGIN IMMEDIATE transaction, i.e. while holding the ledger's only write lock.
// On 2026-09-03 that held the lock for the length of a table scan on every
// proxy charge, every other writer waited out busy_timeout and returned
// SQLITE_BUSY, and the drained connection pool stalled 1ms reads for 5–15s —
// operations read it as a "database deadlock" and rolled v0.36.99 back.
// TestIdemLookupsUseThePartialIndex pins the plan.
const idemLookupSQL = `SELECT id, workspace_id, user_id, kind, amount_usd FROM wallet_tx WHERE idem_key = ? AND idem_key <> ''`

func idemSettledSQL(placeholders string) string {
	return `SELECT idem_key, amount_usd FROM wallet_tx WHERE idem_key IN (` + placeholders + `) AND idem_key <> ''`
}

// rowQuerier is satisfied by both *DB and *sql.Tx, so the replay lookup runs
// unchanged inside the movement transaction and standalone after losing the
// insert race.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// findIdemRow resolves an already-settled idempotency key into a replay result.
// found=false means the key is unused and the caller should perform the
// movement.
func findIdemRow(ctx context.Context, q rowQuerier, req IdemChargeReq, kind string) (IdemChargeResult, bool, error) {
	var (
		id      int64
		wsID    int64
		userID  int64
		rowKind string
		amount  float64
		liveBal float64
		haveBal bool
	)
	scanErr := q.QueryRowContext(ctx,
		idemLookupSQL, req.IdempotencyKey).Scan(&id, &wsID, &userID, &rowKind, &amount)
	if scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return IdemChargeResult{}, false, nil
		}
		return IdemChargeResult{}, false, scanErr
	}
	// A replay must describe the SAME movement. Anything else is a key
	// collision, and honouring it would move money on the strength of a
	// caller-supplied string.
	if rowKind != kind || wsID != req.WorkspaceID || (req.UserID != 0 && userID != req.UserID) {
		return IdemChargeResult{}, false, ErrIdemConflict
	}
	if err := q.QueryRowContext(ctx, `SELECT balance_usd FROM workspaces WHERE id = ?`, wsID).Scan(&liveBal); err == nil {
		haveBal = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return IdemChargeResult{}, false, err
	}
	moved := math.Abs(amount)
	res := IdemChargeResult{
		TxID:       id,
		ChargedUSD: moved,
		Clamped:    kind == TxKindCharge && moved < req.AmountUSD,
		Replayed:   true,
	}
	if haveBal {
		res.NewBalanceUSD = liveBal
	}
	return res, true, nil
}

// isUniqueViolation reports whether err is SQLite refusing a duplicate key.
// Matched on the message rather than the driver's error type: package db is
// deliberately driver-agnostic at the call sites, and modernc's error carries
// the standard SQLite text.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// SettledIdemKeys reports which of the given idempotency keys already have a
// ledger row, mapping each to the magnitude that was moved.
//
// The replay protection itself does not need this — walletIdem detects a
// duplicate on its own. It exists so a REPORT can tell a settled window from an
// unsettled one. Reconciliation reads the request log, and nothing there
// records that a charge was later recovered, so a dry run over an
// already-repaired window looks identical to one over a fresh outage. Without
// this an operator's only way to find out is to run --apply and read what it
// says, which is the wrong direction for a command whose dry run exists to be
// the safe thing to do first.
func (db *DB) SettledIdemKeys(ctx context.Context, keys []string) (map[string]float64, error) {
	out := make(map[string]float64, len(keys))
	// Empty keys are dropped, not queried. Every ordinary in-process charge
	// carries idem_key='' (that is what the v22 index is partial on), so an
	// empty needle matches a haystack of unrelated rows and would report an
	// unsettled group as already repaired — silently skipping money that is
	// still owed.
	args := make([]any, 0, len(keys))
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			continue
		}
		args = append(args, k)
	}
	if len(args) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat("?,", len(args)-1) + "?"
	rows, err := db.QueryContext(ctx, idemSettledSQL(placeholders), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var k string
		var amt float64
		if err := rows.Scan(&k, &amt); err != nil {
			return nil, err
		}
		if amt < 0 {
			amt = -amt
		}
		out[k] = amt
	}
	return out, rows.Err()
}
