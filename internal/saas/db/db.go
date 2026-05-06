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

	_ "modernc.org/sqlite"
)

// DB wraps *sql.DB with helper methods grouped by domain (users, tokens, ...).
type DB struct {
	*sql.DB
	path string
}

// Open opens (or creates) the SQLite file at path with WAL enabled and runs
// any pending migrations.
//
// synchronous=FULL: forces an fsync on every commit. Slightly slower than
// NORMAL (one extra fsync per tx) but the only setting that survives raw
// power loss without losing committed wallet/payment transactions. SaaS
// traffic is nowhere near the throughput where the difference matters.
//
// File mode is force-chmoded to 0600 after open so the wallet ledger is
// not world-readable even if the filesystem default umask was lax.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)", path)
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection is fine — WAL serializes writes and sql.DB handles
	// pooling automatically. SQLite doesn't benefit from large pools.
	sdb.SetMaxOpenConns(8)
	sdb.SetMaxIdleConns(4)
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
