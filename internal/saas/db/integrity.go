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
//
// The 2026-08-22 incident showed what detection alone is worth. At 12:51:55
// something outside this process unlinked the live saas.db-wal and
// saas.db-shm; the server went on holding the deleted inodes, its WAL index
// now describing a file no directory entry pointed at, and every read came
// back SQLITE_IOERR_SHORT_READ. Detection worked exactly as designed — the
// alert mailed at 12:55:19 — and then the server sat in the broken state for
// another 17 minutes waiting for a human, dropping 199 charges and refusing
// 883 requests, until someone restarted it.
//
// The restart is what fixed it, and the reason it fixed it is narrow: the
// database FILE was never damaged (quick_check on a copy returned ok, and the
// ledger had not lost a row). Only the process's file handles were stale. So
// the third piece is:
//
//  3. Recovery. On seeing a corruption error, recycle the connection pool.
//     Every new connection re-open()s the path and lands on whatever inode the
//     directory entry names now, which is what the restart accomplished — minus
//     the restart. Then re-run quick_check to find out which kind of failure
//     this was: if it passes the file was always fine and the process has
//     healed itself; if it still fails the file really is damaged, no amount of
//     reconnecting will help, and the operator alert stands.

// recoverCooldown spaces out self-heal attempts. A broken database emits
// errors continuously and every one of them routes here; without this the pool
// would be recycled thousands of times a minute.
const recoverCooldown = 30 * time.Second

// maxRecoverAttempts bounds consecutive failed heals. Recycling connections
// fixes stale handles and nothing else, so if three rounds have not restored
// the database the fault is in the file and retrying forever only adds churn
// to an incident a human is already needed for.
const maxRecoverAttempts = 3

// recoverAttemptReset forgets the failed-attempt count once the database has
// been quiet this long, so a fresh incident weeks later still gets its three
// tries instead of inheriting an exhausted budget from the last one.
const recoverAttemptReset = time.Hour

// connRecycleGrace is how long the pool is held at zero idle connections. A
// connection checked out by an in-flight query cannot be closed underneath it;
// it is closed when returned, but only while MaxIdleConns is still 0 and the
// short ConnMaxLifetime still marks it expired. This window is what lets those
// in-flight connections drain rather than being handed straight back out.
const connRecycleGrace = 250 * time.Millisecond

// recoverState throttles self-heal. Separate from corruptState because the
// alert cooldown (hourly, about not spamming a human) and the recovery
// cooldown (30s, about not thrashing the pool) answer different questions.
type recoverState struct {
	inFlight    atomic.Bool
	lastAttempt atomic.Int64 // unix seconds
	attempts    atomic.Int32
	heals       atomic.Int64
}

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
	// Resolved marks the all-clear that follows a successful self-heal, rather
	// than a new failure. Handlers should say so differently: an operator who
	// has already been paged needs to know the thing stopped bleeding, and a
	// second message that reads identically to the first is worse than none.
	Resolved bool
}

// corruptState carries alert throttling and the tripped flag. It lives on DB
// so both the scheduled check and Report() share one cooldown and one latch.
type corruptState struct {
	lastAlert atomic.Int64 // unix seconds
	tripped   atomic.Bool
	handler   atomic.Pointer[func(IntegrityAlert)]
}

// Corrupted reports whether the database is currently believed to be damaged.
// Callers that would rather fail a request than write into a damaged file can
// consult it.
//
// It clears only when a self-heal has recycled the connection pool AND a
// following quick_check passed — that is, when the evidence says the file was
// never the problem. A genuinely corrupt SQLite file does not repair itself,
// so in that case the flag stays set until the operator intervenes.
func (db *DB) Corrupted() bool { return db.corrupt.tripped.Load() }

// Heals reports how many times the pool has been recycled back to health since
// process start. Exposed for the admin panel and for tests, and worth watching:
// a database that self-heals repeatedly is one something keeps yanking files
// out from under, which is an operational problem the process cannot fix.
func (db *DB) Heals() int64 { return db.recov.heals.Load() }

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
		log.Errorf("saas-db: CORRUPTION DETECTED (%s): %s — attempting self-heal; if that fails the database needs operator attention, see docs for the recover procedure", a.Source, a.Detail)
	}

	// Before the alert cooldown, which returns early below: the mail is
	// throttled to hourly because a human only needs telling once, but the
	// repair must still be attempted on later errors — recoverNow has its own,
	// much shorter, throttle for that.
	db.scheduleRecover(a)

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

// recycleConns closes every pooled connection and lets the pool rebuild.
//
// This is the whole repair. database/sql has no "drop all connections" call,
// but SetMaxIdleConns(0) synchronously closes the idle ones, and a
// ConnMaxLifetime of one nanosecond marks the checked-out ones expired so they
// are closed on return instead of being reused. What matters is that the next
// connection is a fresh open() of db.path: a handle to the deleted inode is
// replaced by a handle to the file that path names now, along with a rebuilt
// -wal and -shm.
//
// The pool sizes are restored afterwards, so a heal leaves no trace in the
// server's steady-state behaviour.
func (db *DB) recycleConns() {
	db.SetConnMaxLifetime(time.Nanosecond)
	db.SetMaxIdleConns(0)
	time.Sleep(connRecycleGrace)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(0)
}

// scheduleRecover kicks off at most one self-heal goroutine.
//
// It must not block: the only live-traffic caller is Report(), which runs in a
// defer on the billing hot path. The CAS also means the thousands of errors a
// broken database produces collapse into one attempt rather than thousands of
// goroutines all recycling the same pool.
func (db *DB) scheduleRecover(a IntegrityAlert) {
	if !db.recov.inFlight.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer db.recov.inFlight.Store(false)
		db.recoverNow(a)
	}()
}

// recoverNow performs one throttled self-heal attempt. Runs on its own
// goroutine; never called inline from a request path.
func (db *DB) recoverNow(a IntegrityAlert) {
	now := time.Now().Unix()
	last := db.recov.lastAttempt.Load()
	if last != 0 {
		since := now - last
		if since < int64(recoverCooldown/time.Second) {
			return
		}
		// A long quiet spell means this is a new incident, not a continuation
		// of the one that used up the attempt budget.
		if since > int64(recoverAttemptReset/time.Second) {
			db.recov.attempts.Store(0)
		}
	}
	db.recov.lastAttempt.Store(now)

	if n := db.recov.attempts.Add(1); n > maxRecoverAttempts {
		log.Errorf("saas-db: self-heal given up after %d attempts — the file itself is damaged and needs the operator recovery procedure", maxRecoverAttempts)
		return
	}

	log.Warnf("saas-db: corruption reported by %s; recycling the connection pool to rule out stale file handles", a.Source)
	db.recycleConns()

	// Generous timeout: quick_check on the production file runs in well under
	// a second, but the pool has just been emptied and a heal must not be
	// abandoned because the machine was briefly busy.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := db.QuickCheck(ctx); err != nil {
		log.Errorf("saas-db: self-heal did NOT restore the database (%v) — this is real file damage, not stale handles; operator action required", err)
		return
	}

	// quick_check passing on a freshly-opened connection means the bytes on
	// disk were fine all along and the process had simply lost its grip on
	// them. Clear the latch: billing can resume.
	db.recov.attempts.Store(0)
	n := db.recov.heals.Add(1)
	db.corrupt.tripped.Store(false)
	log.Warnf("saas-db: SELF-HEALED by recycling the connection pool (heal #%d) — the database file was intact; the process was holding stale handles, most likely because something outside it unlinked the live -wal/-shm", n)

	db.notify(IntegrityAlert{
		Source:   "self-heal",
		Detail:   fmt.Sprintf("recovered after %s reported: %s", a.Source, a.Detail),
		At:       time.Now(),
		Resolved: true,
	})
}

// notify fires the registered handler without touching the alert cooldown.
// The all-clear is not rate-limited against the failure that preceded it —
// suppressing "it is fixed" because "it is broken" was recent is precisely
// backwards.
func (db *DB) notify(a IntegrityAlert) {
	if h := db.corrupt.handler.Load(); h != nil && *h != nil {
		go (*h)(a)
	}
}
