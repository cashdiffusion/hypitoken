// Package db opens the SQLite store and applies migrations. The DB is the
// single source of truth for users, per-user tokens, pricing groups, wallet
// transactions, Alipay orders, and model health checks. Concurrent writers
// are serialized by SQLite WAL.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	// _ "modernc.org/sqlite" registers the SQLite database driver.
	_ "modernc.org/sqlite"
)

// Connection-pool sizes. Named because recycleConns (integrity.go) has to put
// them back after temporarily driving MaxIdleConns to zero, and a self-heal
// that silently resized the pool would be a worse bug than the one it fixes.
const (
	maxOpenConns = 8
	maxIdleConns = 4
)

// DB wraps *sql.DB with helper methods grouped by domain (users, tokens, ...).
type DB struct {
	*sql.DB
	path string

	// corrupt tracks integrity state: the latch set once corruption is seen,
	// the alert cooldown, and the operator-notification handler. See
	// integrity.go.
	corrupt corruptState

	// recov throttles connection-pool self-heal attempts. See integrity.go.
	recov recoverState
}

// Open opens (or creates) the SQLite file at path with WAL enabled and runs
// any pending migrations.
//
// synchronous=FULL: forces an fsync on every commit. Slightly slower than
// NORMAL (one extra fsync per tx) but the only setting that survives raw
// power loss without losing committed wallet/payment transactions. SaaS
// traffic is nowhere near the throughput where the difference matters.
//
// _txlock=immediate: every BeginTx opens BEGIN IMMEDIATE rather than Go's
// default BEGIN DEFERRED. This is load-bearing, not a tuning knob. A deferred
// transaction takes a read lock on its first SELECT and only asks for the write
// lock at its first write; when another connection already holds the write
// lock, SQLite fails that upgrade with SQLITE_BUSY *immediately and
// deliberately ignores busy_timeout* — backing off would deadlock two readers
// both waiting to upgrade. So busy_timeout cannot help a deferred read→write
// transaction, which is exactly the shape of Charge (read balance, then write
// balance + ledger row). In production this failed ~29% of all charge attempts
// under normal concurrency (7.8k lost charges in 24h) with no retry anywhere —
// requests served, never billed. With IMMEDIATE the write lock is taken at
// BEGIN, so contention waits on busy_timeout instead of erroring out.
//
// Every BeginTx site in this module writes, so nothing pays for this
// serialization needlessly; plain Query/Exec outside a transaction is
// unaffected. busy_timeout is 10s (was 5s) purely for headroom: charge
// transactions are sub-millisecond, so the queue drains far below that.
// TestConcurrentChargesDoNotReturnBusy guards the flag.
//
// File mode is force-chmoded to 0600 after open so the wallet ledger is
// not world-readable even if the filesystem default umask was lax.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)", path)
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection is fine — WAL serializes writes and sql.DB handles
	// pooling automatically. SQLite doesn't benefit from large pools.
	sdb.SetMaxOpenConns(maxOpenConns)
	sdb.SetMaxIdleConns(maxIdleConns)
	if err := sdb.Ping(); err != nil {
		return nil, err
	}
	// Tighten file modes on the live DB and its WAL/SHM siblings. Best-effort
	// — siblings may not exist yet; they'll be created with the same default
	// the OS umask gives the parent process, so we re-chmod here.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(path+suffix, 0o600)
	}
	db := &DB{DB: sdb, path: path}
	if err := db.migrate(); err != nil {
		_ = sdb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func (db *DB) Path() string { return db.path }
