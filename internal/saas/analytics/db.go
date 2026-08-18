package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"

	// _ "modernc.org/sqlite" registers the SQLite database driver.
	_ "modernc.org/sqlite"
)

// Visitor tracking used to live in saas.db alongside the wallet. On
// 2026-08-18 that database developed page corruption during a burst of
// signup traffic, and the damaged pages sat in web_sessions, user_profiles,
// email_codes and channel_visits — the write-heavy, low-value tables — while
// the blast radius covered the ledger too.
//
// Analytics is the highest-write, lowest-value data in the system: every
// visitor pageview, every dwell ping. There is no reason for it to share a
// file, a WAL, or a fsync budget with money. Here it gets its own database,
// so a corrupt tracking table can never again take billing down with it, and
// the wallet's WAL stops absorbing traffic that nobody would pay to recover.
//
// synchronous=NORMAL, not FULL: losing the last few pageviews to a power cut
// is free, and it drops an fsync from the hottest write path in the product.
// The wallet keeps FULL — see internal/saas/db.Open for why that one can't.

// DefaultFileName is the analytics database's filename, created next to
// saas.db unless configured otherwise.
const DefaultFileName = "analytics.db"

// Open opens (or creates) the analytics SQLite database at path and ensures
// its schema exists. The returned Service owns the handle; call Close when
// shutting down.
func Open(path string) (*Service, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Tracking writes are short and frequent; a small pool is plenty and
	// keeps SQLite's writer queue shallow.
	sdb.SetMaxOpenConns(4)
	sdb.SetMaxIdleConns(2)
	if err := sdb.Ping(); err != nil {
		_ = sdb.Close()
		return nil, err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(path+suffix, 0o600)
	}
	if err := ensureSchema(sdb); err != nil {
		_ = sdb.Close()
		return nil, fmt.Errorf("analytics schema: %w", err)
	}
	return &Service{db: sdb, owned: true}, nil
}

// Close releases the handle when the Service owns its database. A Service
// built with New (sharing a caller's handle) never closes it.
func (s *Service) Close() error {
	if s == nil || s.db == nil || !s.owned {
		return nil
	}
	return s.db.Close()
}

// schema mirrors what SaaS migration v7 created inside saas.db, so rows moved
// across by ImportFrom land in an identical shape.
const schema = `
CREATE TABLE IF NOT EXISTS web_sessions (
    session_id      TEXT PRIMARY KEY,
    visitor_id      TEXT NOT NULL DEFAULT '',
    landing_path    TEXT NOT NULL DEFAULT '',
    first_action    TEXT NOT NULL DEFAULT '',
    source          TEXT NOT NULL DEFAULT '',
    referrer_domain TEXT NOT NULL DEFAULT '',
    pageviews       INTEGER NOT NULL DEFAULT 0,
    actions         INTEGER NOT NULL DEFAULT 0,
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    started_at      INTEGER NOT NULL,
    last_seen       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_web_sessions_started ON web_sessions(started_at);
CREATE INDEX IF NOT EXISTS idx_web_sessions_visitor ON web_sessions(visitor_id);

CREATE TABLE IF NOT EXISTS web_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    visitor_id TEXT NOT NULL DEFAULT '',
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    seq        INTEGER NOT NULL DEFAULT 0,
    ts         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_web_events_session ON web_events(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_web_events_ts ON web_events(ts);
`

func ensureSchema(sdb *sql.DB) error {
	_, err := sdb.Exec(schema)
	return err
}

// ImportFrom copies visitor history out of the old shared database (saas.db)
// into this one, once, when this database is still empty. It is a no-op on
// every subsequent start.
//
// Rows are copied through Go rather than by ATTACH-ing the two files: a
// cross-database transaction is exactly the shape of the upstream SQLite
// rollback bug fixed in modernc.org/sqlite v1.56.0, and there is no reason to
// take that risk against the live wallet database to move tracking data.
//
// The source tables are deliberately left in place. Dropping them belongs in a
// later release, after this one has been running long enough to trust — the
// same ordering discipline that keeps a config key alive until the new binary
// is deployed.
func (s *Service) ImportFrom(ctx context.Context, src *sql.DB) error {
	if s == nil || s.db == nil || src == nil {
		return nil
	}
	var existing int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM web_sessions`).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return nil // already populated; nothing to do
	}

	// A source without the tables (fresh install) is not an error.
	var hasTable int
	if err := src.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='web_sessions'`).Scan(&hasTable); err != nil || hasTable == 0 {
		return nil //nolint:nilerr // a source we can't read just means nothing to import
	}

	sessions, err := s.importSessions(ctx, src)
	if err != nil {
		return fmt.Errorf("import web_sessions: %w", err)
	}
	events, err := s.importEvents(ctx, src)
	if err != nil {
		return fmt.Errorf("import web_events: %w", err)
	}
	if sessions > 0 || events > 0 {
		log.Infof("analytics: imported %d session(s) and %d event(s) from the shared database", sessions, events)
	}
	return nil
}

func (s *Service) importSessions(ctx context.Context, src *sql.DB) (int, error) {
	rows, err := src.QueryContext(ctx,
		`SELECT session_id, visitor_id, landing_path, first_action, source, referrer_domain,
		        pageviews, actions, duration_ms, started_at, last_seen
		   FROM web_sessions`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO web_sessions
		   (session_id, visitor_id, landing_path, first_action, source, referrer_domain,
		    pageviews, actions, duration_ms, started_at, last_seen)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stmt.Close() }()

	n := 0
	for rows.Next() {
		var (
			sid, vid, landing, first, source, domain string
			pv, act, dur, started, seen              int64
		)
		if err := rows.Scan(&sid, &vid, &landing, &first, &source, &domain, &pv, &act, &dur, &started, &seen); err != nil {
			return 0, err
		}
		if _, err := stmt.ExecContext(ctx, sid, vid, landing, first, source, domain, pv, act, dur, started, seen); err != nil {
			return 0, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

func (s *Service) importEvents(ctx context.Context, src *sql.DB) (int, error) {
	rows, err := src.QueryContext(ctx,
		`SELECT id, session_id, visitor_id, kind, name, seq, ts FROM web_events`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO web_events (id, session_id, visitor_id, kind, name, seq, ts)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stmt.Close() }()

	n := 0
	for rows.Next() {
		var (
			id, seq, ts             int64
			sid, vid, kind, evtName string
		)
		if err := rows.Scan(&id, &sid, &vid, &kind, &evtName, &seq, &ts); err != nil {
			return 0, err
		}
		if _, err := stmt.ExecContext(ctx, id, sid, vid, kind, evtName, seq, ts); err != nil {
			return 0, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
