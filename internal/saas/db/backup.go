package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// SnapshotTo writes a consistent, self-contained copy of the SQLite database
// to dst using SQLite's `VACUUM INTO`. Unlike a raw file copy, this is
// transactional — readers/writers continue working and the destination
// gets a single point-in-time snapshot with no WAL/SHM siblings to manage.
//
// `dst` is created with mode 0600 (after the fact — VACUUM INTO uses the
// process's default umask, which we then tighten).
func (db *DB) SnapshotTo(ctx context.Context, dst string) error {
	if dst == "" {
		return fmt.Errorf("snapshot: empty destination")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	// VACUUM INTO refuses to overwrite. If a stale file from a previous run
	// exists (e.g. crash during the previous attempt), drop it first.
	_ = os.Remove(dst)

	// SQLite has no parameter binding for VACUUM INTO's path, so we have to
	// inline-quote it. Single-quote escaping is enough since the destination
	// is operator-controlled, not user-controlled.
	q := fmt.Sprintf(`VACUUM INTO '%s'`, strings.ReplaceAll(dst, "'", "''"))
	if _, err := db.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("vacuum into %s: %w", dst, err)
	}
	_ = os.Chmod(dst, 0o600)
	return nil
}

// RunDailyBackups spins a goroutine that takes a daily snapshot to
// `<dbdir>/backups/saas-YYYY-MM-DD.db` and prunes anything older than
// `keep` days. Cancel `ctx` to stop.
//
// Snapshots are atomic via VACUUM INTO so request handlers don't see a
// pause; the file is also a clean self-contained DB (no -wal / -shm
// siblings) which makes off-host shipping a single-file copy.
func (db *DB) RunDailyBackups(ctx context.Context, keep int) {
	if keep <= 0 {
		keep = 30
	}
	dir := filepath.Join(filepath.Dir(db.path), "backups")

	// Take one immediately on startup so a fresh deploy isn't unprotected
	// for up to 24h before the first scheduled run.
	db.runOneBackup(ctx, dir, keep)

	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			db.runOneBackup(ctx, dir, keep)
		}
	}
}

func (db *DB) runOneBackup(ctx context.Context, dir string, keep int) {
	stamp := time.Now().UTC().Format("2006-01-02")
	dst := filepath.Join(dir, fmt.Sprintf("saas-%s.db", stamp))
	if err := db.SnapshotTo(ctx, dst); err != nil {
		log.Warnf("saas-db: daily backup failed: %v", err)
		return
	}
	log.Infof("saas-db: daily snapshot → %s", dst)
	pruneOldBackups(dir, keep)
}

// pruneOldBackups deletes saas-*.db files beyond the most recent `keep`.
// Migration backups (saas.db.backup-vN-*) are left alone — they live next
// to the DB, not in the backups/ dir.
func pruneOldBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type stamped struct {
		name string
		mod  time.Time
	}
	var snaps []stamped
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "saas-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		snaps = append(snaps, stamped{name: e.Name(), mod: info.ModTime()})
	}
	if len(snaps) <= keep {
		return
	}
	// Newest first — keep the head, prune the tail.
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].mod.After(snaps[j].mod) })
	for _, s := range snaps[keep:] {
		_ = os.Remove(filepath.Join(dir, s.name))
	}
}
