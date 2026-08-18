package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// The 2026-08-18 incident: saas.db developed page-level corruption somewhere
// inside a 10-hour window, and nothing noticed. The server kept serving and
// kept trying to bill against a broken b-tree for ~20 minutes, logging each
// failure at warning level among thousands of ordinary request lines, until a
// human discovered it by failing to log in. Every charge attempted in that
// window was lost.
//
// Two things were missing and are supplied here:
//
//  1. Detection. A periodic PRAGMA quick_check that runs on the server's own
//     connection pool — no second process, no CLI touching a live WAL database
//     (that is how a *previous* incident was caused). quick_check is the cheap
//     variant: it verifies b-tree structure and skips the index cross-checks
//     integrity_check does, which is the right trade for something that runs
//     hourly on a 300 MB file.
//
//  2. Escalation. A corruption error surfacing from ordinary traffic is worth
//     more than a warning line. Report() folds those runtime errors into the
//     same alerting path as the scheduled check, so whichever notices first
//     wakes somebody up.

// corruptionAlertCooldown throttles repeat alerts. A corrupt database produces
// errors continuously; one mail per hour is a signal, one per charge is a
// mail-bomb that gets the sender domain blocked.
const corruptionAlertCooldown = time.Hour

// DefaultIntegrityInterval is how often RunIntegrityChecks probes. Hourly
// bounds the blind window at one hour without meaningfully loading the DB —
// quick_check on the production 300 MB file takes well under a second.
const DefaultIntegrityInterval = time.Hour

// IntegrityAlert describes a detected corruption event, passed to the handler
// registered with RunIntegrityChecks.
type IntegrityAlert struct {
	// Source is where the problem surfaced: "scheduled check" or "live traffic".
	Source string
	// Detail is the SQLite error or the quick_check output.
	Detail string
	// At is when it was observed.
	At time.Time
}

// corruptState carries alert throttling and the tripped flag. It lives on DB
// so both the scheduled check and Report() share one cooldown and one latch.
type corruptState struct {
	lastAlert atomic.Int64 // unix seconds
	tripped   atomic.Bool
	handler   atomic.Pointer[func(IntegrityAlert)]
}

// Corrupted reports whether corruption has been observed on this database
// since process start. Callers that would rather fail a request than write
// into a damaged file can consult it; it never resets on its own, because a
// corrupt SQLite file does not heal and the operator has to intervene.
func (db *DB) Corrupted() bool { return db.corrupt.tripped.Load() }

// QuickCheck runs PRAGMA quick_check and returns an error if the database
// does not report "ok". It uses the server's existing pool, so it is safe to
// call at any time against a live database.
func (db *DB) QuickCheck(ctx context.Context) error {
	rows, err := db.QueryContext(ctx, `PRAGMA quick_check(1)`)
	if err != nil {
		return fmt.Errorf("quick_check: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var lines []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return fmt.Errorf("quick_check scan: %w", err)
		}
		lines = append(lines, s)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("quick_check rows: %w", err)
	}
	if len(lines) == 1 && strings.EqualFold(strings.TrimSpace(lines[0]), "ok") {
		return nil
	}
	return fmt.Errorf("quick_check reported: %s", strings.Join(lines, "; "))
}

// IsCorruptionError reports whether err is one of SQLite's "the file is
// damaged or unreadable" results, as opposed to an ordinary constraint or
// busy error.
//
// The incident produced all three of these in rotation, which is itself
// diagnostic: SQLITE_IOERR_SHORT_READ (522) came first — the WAL index
// pointing past the end of the WAL file — and only then did reads start
// landing on garbage pages and returning SQLITE_CORRUPT (11) and
// SQLITE_NOTADB (26). Matching on message text rather than the driver's error
// type keeps this working across driver versions, which wrap errors
// differently; the strings come from SQLite itself and are stable.
func IsCorruptionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"database disk image is malformed",
		"file is not a database",
		"disk i/o error",
		"database corruption",
		"malformed database schema",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// Report escalates an error observed during ordinary traffic. Non-corruption
// errors are ignored, so callers can hand it every error they see without
// filtering first. Safe to call from any goroutine and before
// RunIntegrityChecks has been started.
func (db *DB) Report(err error) {
	if !IsCorruptionError(err) {
		return
	}
	db.raise(IntegrityAlert{Source: "live traffic", Detail: err.Error(), At: time.Now()})
}

// raise latches the tripped flag, logs at error level, and fires the handler
// at most once per cooldown.
func (db *DB) raise(a IntegrityAlert) {
	first := db.corrupt.tripped.CompareAndSwap(false, true)
	if first {
		log.Errorf("saas-db: CORRUPTION DETECTED (%s): %s — the database needs operator attention; see docs for the recover procedure", a.Source, a.Detail)
	}

	now := a.At.Unix()
	last := db.corrupt.lastAlert.Load()
	if !first && now-last < int64(corruptionAlertCooldown/time.Second) {
		return
	}
	if !db.corrupt.lastAlert.CompareAndSwap(last, now) {
		return // another goroutine is sending this one
	}
	if h := db.corrupt.handler.Load(); h != nil && *h != nil {
		go (*h)(a)
	}
}

// RunIntegrityChecks probes the database every interval and calls onAlert when
// corruption is found, by the scheduled check or by Report() from live
// traffic. Blocks until ctx is cancelled; run it in a goroutine.
//
// onAlert may be nil (log-only). It is called from a fresh goroutine and is
// rate-limited to one call per corruptionAlertCooldown across both sources.
func (db *DB) RunIntegrityChecks(ctx context.Context, interval time.Duration, onAlert func(IntegrityAlert)) {
	if interval <= 0 {
		interval = DefaultIntegrityInterval
	}
	if onAlert != nil {
		db.corrupt.handler.Store(&onAlert)
	}

	// Probe once at startup. A database that came up already damaged — the
	// shape this incident would have had if it had been noticed on the next
	// restart rather than by a failed login — should not wait an hour to say so.
	db.checkOnce(ctx)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			db.checkOnce(ctx)
		}
	}
}

func (db *DB) checkOnce(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	start := time.Now()
	err := db.QuickCheck(cctx)
	if err == nil {
		log.Debugf("saas-db: quick_check ok (%s)", time.Since(start).Round(time.Millisecond))
		return
	}
	// A cancelled context is the server shutting down, not a damaged file.
	if errors.Is(cctx.Err(), context.Canceled) {
		return
	}
	db.raise(IntegrityAlert{Source: "scheduled check", Detail: err.Error(), At: time.Now()})
}
