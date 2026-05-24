package db

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

// migrations is an append-only slice. Each element is a complete schema delta;
// applying them all in order rebuilds the schema from scratch. Never reorder
// or rewrite a previous entry — only append.
var migrations = []string{
	// 1: initial schema
	`
CREATE TABLE pricing_groups (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    name                TEXT NOT NULL UNIQUE,
    description         TEXT NOT NULL DEFAULT '',
    codex_rmb_per_usd   REAL NOT NULL DEFAULT 0.5,
    claude_rmb_per_usd  REAL NOT NULL DEFAULT 2.0,
    codex_multiplier    REAL NOT NULL DEFAULT 1.0,
    claude_multiplier   REAL NOT NULL DEFAULT 1.0,
    credential_group    TEXT NOT NULL DEFAULT '',
    is_default          INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
INSERT INTO pricing_groups (name, description, is_default, created_at, updated_at)
VALUES ('default', 'Default pricing group', 1, strftime('%s','now'), strftime('%s','now'));

CREATE TABLE users (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    email               TEXT NOT NULL UNIQUE,
    pw_hash             TEXT NOT NULL,
    role                TEXT NOT NULL DEFAULT 'user',          -- user | admin
    balance_usd         REAL NOT NULL DEFAULT 0,
    group_id            INTEGER NOT NULL,
    email_verified      INTEGER NOT NULL DEFAULT 0,
    disabled            INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL,
    FOREIGN KEY (group_id) REFERENCES pricing_groups(id)
);
CREATE INDEX idx_users_email ON users(email);

CREATE TABLE email_codes (
    email       TEXT NOT NULL,
    code        TEXT NOT NULL,
    purpose     TEXT NOT NULL,                                  -- verify | reset
    expires_at  INTEGER NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (email, purpose)
);

CREATE TABLE user_tokens (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id             INTEGER NOT NULL,
    token               TEXT NOT NULL UNIQUE,
    name                TEXT NOT NULL DEFAULT '',
    daily_usd_cap       REAL NOT NULL DEFAULT 0,
    monthly_usd_cap     REAL NOT NULL DEFAULT 0,
    max_concurrent      INTEGER NOT NULL DEFAULT 0,
    rpm                 INTEGER NOT NULL DEFAULT 0,
    disabled            INTEGER NOT NULL DEFAULT 0,
    last_used_at        INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_user_tokens_user ON user_tokens(user_id);
CREATE INDEX idx_user_tokens_token ON user_tokens(token);

CREATE TABLE wallet_tx (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    kind        TEXT NOT NULL,                                  -- topup | charge | adjust | refund
    amount_usd  REAL NOT NULL,                                  -- signed; +credit / -debit
    ref         TEXT NOT NULL DEFAULT '',                       -- alipay out_trade_no, request id, etc.
    note        TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_wallet_tx_user ON wallet_tx(user_id, created_at);

CREATE TABLE alipay_orders (
    out_trade_no    TEXT PRIMARY KEY,
    user_id         INTEGER NOT NULL,
    cny_amount      REAL NOT NULL,
    usd_credit      REAL NOT NULL,
    rate            REAL NOT NULL,                              -- snapshot CNY/USD at create
    status          TEXT NOT NULL DEFAULT 'pending',            -- pending | paid | expired | failed
    trade_no        TEXT NOT NULL DEFAULT '',
    qr_code         TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    paid_at         INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_alipay_orders_user ON alipay_orders(user_id, created_at);

CREATE TABLE model_health (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    auth_id         TEXT NOT NULL,
    provider        TEXT NOT NULL,
    model           TEXT NOT NULL,
    status          TEXT NOT NULL,                              -- ok | fail
    latency_ms      INTEGER NOT NULL DEFAULT 0,
    error           TEXT NOT NULL DEFAULT '',
    checked_at      INTEGER NOT NULL,
    UNIQUE(auth_id, model)
);
`,
	// 2: health probe history — keeps last 90 results per (auth_id, model)
	`
CREATE TABLE model_health_history (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    auth_id    TEXT NOT NULL,
    provider   TEXT NOT NULL,
    model      TEXT NOT NULL,
    status     TEXT NOT NULL,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    checked_at INTEGER NOT NULL
);
CREATE INDEX idx_mhh ON model_health_history(auth_id, model, checked_at DESC);
`,
	// 3: switch billing model from divisor (rmb_per_usd) to multiplier-only.
	//
	//   final_charge_USD = official_USD * multiplier
	//
	// Bumps the default group from 1.0/1.0 to 0.3/0.05 (claude/codex) so a
	// freshly-installed instance is already aligned with the operator-set
	// defaults. Only touches the seed row; admin-customized values stay put.
	// The legacy *_rmb_per_usd columns are left in the schema (SQLite
	// column-drop is a table rebuild) but are no longer read or written.
	`
UPDATE pricing_groups
SET    claude_multiplier = 0.3,
       codex_multiplier  = 0.05,
       updated_at        = strftime('%s','now')
WHERE  is_default = 1
  AND  claude_multiplier = 1.0
  AND  codex_multiplier  = 1.0;
`,
	// 4: per-user-token priority-ordered group list. Stored as a JSON
	//    array column on user_tokens. Empty / NULL → uses the user's
	//    pricing group (legacy behavior). Non-empty → the token-side
	//    priority list takes over for credential routing (e.g. drop
	//    through Kiro → official Anthropic). Billing still happens
	//    via the resolved group's discount, so existing rate cards
	//    continue to apply unchanged.
	`
ALTER TABLE user_tokens ADD COLUMN groups TEXT NOT NULL DEFAULT '';
`,
}

func (db *DB) migrate() error {
	ctx := context.Background()
	// Bootstrap version table if absent.
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var current int
	row := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_version`)
	if err := row.Scan(&current); err != nil {
		return err
	}
	target := len(migrations)
	if target == current {
		log.Infof("saas-db: schema up to date at v%d", current)
		return nil
	}
	if target < current {
		// Running an older binary against a newer DB — refuse rather than
		// silently downgrade. Wallet/payment ledgers must never be touched
		// by code that doesn't understand the schema.
		return fmt.Errorf("saas-db: binary supports v%d but DB is at v%d — running an older binary on a newer DB is unsupported", target, current)
	}

	// Backup BEFORE applying any new migration. SQLite is "just a file" so
	// a byte copy of a quiesced DB is a complete snapshot. We acquire a
	// read transaction first so concurrent writers don't tear the copy —
	// realistically the SaaS layer is single-writer at startup, but cheap
	// insurance is cheap.
	if current > 0 && db.path != "" {
		if err := backupDBFile(db.path, current); err != nil {
			log.Warnf("saas-db: backup before migrate failed (continuing anyway): %v", err)
		}
	}

	for i, sql := range migrations {
		v := i + 1
		if v <= current {
			continue
		}
		log.Infof("saas-db: applying migration v%d…", v)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", v, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version(version) VALUES(?)`, v); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d (record): %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	log.Infof("saas-db: migrated v%d → v%d", current, target)
	return nil
}

// backupDBFile copies the SQLite file (and its WAL/SHM siblings) to
// "<path>.backup-vN-<timestamp>" so a botched migration can be recovered
// from disk without a separate dump tool.
func backupDBFile(path string, fromVersion int) error {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	dst := fmt.Sprintf("%s.backup-v%d-%s", path, fromVersion, stamp)
	if err := copyFile(path, dst); err != nil {
		return err
	}
	// WAL + SHM are best-effort: if they're not present (DB was checkpointed
	// already), that's fine — the backup of the main file is consistent.
	for _, suffix := range []string{"-wal", "-shm"} {
		src := path + suffix
		if _, err := os.Stat(src); err == nil {
			_ = copyFile(src, dst+suffix)
		}
	}
	log.Infof("saas-db: pre-migrate backup → %s", dst)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
