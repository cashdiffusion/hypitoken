// One-time cross-origin SSO handoff codes (migration v23).
//
// HypiHub lives on hub.novadiffusion.com, hypitoken on api.novadiffusion.com.
// Different origins: no shared localStorage, no shared cookie. So a session is
// handed across by minting a short-lived code here, letting the browser carry
// it in one redirect, and having HypiHub redeem it server-to-server for a
// freshly-signed JWT (see docs/SPEC.md §12 in the hypihub repo).
//
// Two properties this file exists to guarantee:
//
//  1. The stored value is sha256(raw code), never the code. For its 120-second
//     life a row is equivalent to the user's password, and saas.db is snapshotted
//     daily and shipped off-host. A leaked snapshot must not hand anybody live
//     sessions.
//  2. Redemption is exactly-once under concurrency, and every rejection is
//     indistinguishable from every other. A caller must not be able to probe
//     which codes exist by reading error text.
package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// SSOCodeBytes is the entropy behind a handoff code: 32 bytes, 43 characters
// after base64url. The code travels in a URL query string and is therefore
// exposed to browser history, the Referer header of anything the landing page
// loads, and any proxy log in between — so it is sized to be unguessable
// within its own lifetime by a very wide margin, and kept short-lived because
// that exposure cannot be prevented, only outlived.
const SSOCodeBytes = 32

// SSOCodeTTL is how long a minted code stays redeemable. Long enough for one
// browser redirect plus a server-to-server round trip; short enough that a code
// scraped out of a proxy log is almost certainly already dead.
const SSOCodeTTL = 120 * time.Second

// ssoPruneGrace is how far past expiry a spent/expired row is kept before the
// pruner collects it. Retaining the row briefly is what lets a replay be
// answered as "already used" rather than as a lookup miss, which keeps the
// one-shot property observable in logs during an incident.
const ssoPruneGrace = 3600 * time.Second

// ErrSSOCodeInvalid is the SINGLE sentinel for every redemption failure: no
// such code, expired, or already spent.
//
// Deliberately not three errors. The redeem endpoint is reachable by anything
// holding a service token, and distinguishable failures turn it into an oracle:
// "expired" versus "not found" tells an attacker which of the codes they are
// spraying were real, and "already used" confirms a valid code was intercepted
// too late — useful signal for deciding whether to keep attacking a leak. One
// answer for all three costs nothing and says nothing.
var ErrSSOCodeInvalid = errors.New("sso code is invalid, expired, or already used")

// hashSSOCode is the one place raw → stored conversion happens. Hex-encoded
// sha256, matching how the rest of this schema stores bearer material.
func hashSSOCode(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// CreateSSOCode mints a one-time handoff code for userID and returns it in the
// clear — the ONLY moment the raw value exists outside the browser. It is not
// logged and not recoverable: a lost code is re-minted, never looked up.
//
// returnURL must already have been validated by the caller — exact-origin match
// against the operator's allowlist, plus the shape gate that refuses a fragment
// or a pre-existing `code` parameter (adapter.checkReturnURLShape). It is
// recorded, not re-derived, so an audit can say where a session was handed to;
// the redeem path returns it but does not gate on it.
func (db *DB) CreateSSOCode(ctx context.Context, userID int64, returnURL string, ttl time.Duration) (string, error) {
	if userID <= 0 {
		return "", errors.New("sso: user id is required")
	}
	returnURL = strings.TrimSpace(returnURL)
	if returnURL == "" {
		return "", errors.New("sso: return url is required")
	}
	if ttl <= 0 {
		ttl = SSOCodeTTL
	}

	buf := make([]byte, SSOCodeBytes)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is not a request-level problem; minting a code
		// from a degraded source would be far worse than refusing to mint one.
		return "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)

	now := time.Now()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sso_codes (code, user_id, return_url, created_at, expires_at, used_at)
		 VALUES (?, ?, ?, ?, ?, 0)`,
		hashSSOCode(raw), userID, returnURL, now.Unix(), now.Add(ttl).Unix()); err != nil {
		return "", err
	}
	return raw, nil
}

// RedeemSSOCode spends a code exactly once and returns the user it was minted
// for plus the destination it was minted for.
//
// Everything happens in ONE transaction, and the guard is the UPDATE's
// RowsAffected — not the SELECT above it. Two concurrent redeems of the same
// code can both pass the read; only one can be the transaction whose
// `WHERE used_at = 0` still matches. (The pool opens BEGIN IMMEDIATE, so the
// second waits on the write lock rather than racing inside it, but this code
// must not depend on that: the RowsAffected check is correct under any
// isolation the driver might be configured with later.)
//
// Every failure returns ErrSSOCodeInvalid. See the sentinel's comment.
func (db *DB) RedeemSSOCode(ctx context.Context, rawCode string) (int64, string, error) {
	rawCode = strings.TrimSpace(rawCode)
	if rawCode == "" {
		return 0, "", ErrSSOCodeInvalid
	}
	hashed := hashSSOCode(rawCode)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	var (
		userID    int64
		returnURL string
	)
	err = tx.QueryRowContext(ctx,
		`SELECT user_id, return_url FROM sso_codes
		 WHERE code = ? AND used_at = 0 AND expires_at > ?`,
		hashed, now).Scan(&userID, &returnURL)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, "", ErrSSOCodeInvalid
	case err != nil:
		return 0, "", err
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE sso_codes SET used_at = ? WHERE code = ? AND used_at = 0`, now, hashed)
	if err != nil {
		return 0, "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, "", err
	}
	if n != 1 {
		// Somebody else spent it between the SELECT and here. Not an error
		// worth distinguishing — from the loser's point of view the code was
		// already used, which is exactly what ErrSSOCodeInvalid means.
		return 0, "", ErrSSOCodeInvalid
	}
	if err := tx.Commit(); err != nil {
		return 0, "", err
	}
	return userID, returnURL, nil
}

// PruneSSOCodes deletes rows that expired at or before `before` (a Unix
// second), spent or not, and reports how many went. Codes are tiny and
// short-lived, so this is hygiene rather than a capacity concern — but leaving
// spent bearer-credential rows lying around indefinitely is the kind of thing
// that turns a future backup leak into a bigger one than it needed to be.
func (db *DB) PruneSSOCodes(ctx context.Context, before int64) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM sso_codes WHERE expires_at < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RunSSOCodePrune runs PruneSSOCodes on a ticker until ctx is cancelled,
// collecting anything that expired more than an hour ago. Sits alongside
// RunDailyBackups / RunIntegrityChecks as one more periodic-maintenance
// goroutine; cancel the shared refresher context to stop it.
//
// interval <= 0 falls back to 10 minutes.
func (db *DB) RunSSOCodePrune(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := db.PruneSSOCodes(ctx, time.Now().Add(-ssoPruneGrace).Unix())
			if err != nil {
				log.Warnf("saas-db: sso code prune failed: %v", err)
				continue
			}
			if n > 0 {
				log.Debugf("saas-db: pruned %d expired sso code(s)", n)
			}
		}
	}
}
