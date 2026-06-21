package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// UserProfile is the public-arena identity + running activity counters for a
// user. One row per user, created lazily on first read. display_name is the
// nickname shown on the leaderboard / office; PublicOptIn gates whether it is
// shown under the real name or an anonymous pseudonym.
type UserProfile struct {
	UserID           int64
	DisplayName      string
	NameIsDefault    bool
	PublicOptIn      bool
	LifetimeTokens   int64
	LifetimeRequests int64
	LifetimeInvites  int64
	LastActiveAt     time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const profileCols = `user_id, display_name, name_is_default, public_opt_in, lifetime_tokens, lifetime_requests, lifetime_invites, last_active_at, created_at, updated_at`

func scanProfile(row interface{ Scan(...any) error }) (*UserProfile, error) {
	var p UserProfile
	var nameDefault, optIn int
	var lastActive, created, updated int64
	if err := row.Scan(&p.UserID, &p.DisplayName, &nameDefault, &optIn, &p.LifetimeTokens, &p.LifetimeRequests, &p.LifetimeInvites, &lastActive, &created, &updated); err != nil {
		return nil, err
	}
	p.NameIsDefault = nameDefault != 0
	p.PublicOptIn = optIn != 0
	if lastActive > 0 {
		p.LastActiveAt = time.Unix(lastActive, 0)
	}
	p.CreatedAt = time.Unix(created, 0)
	p.UpdatedAt = time.Unix(updated, 0)
	return &p, nil
}

// defaultNicknamePool seeds the system-assigned nickname (hypi-<Name><digits>).
// Kept deliberately friendly + gender-neutral; the user is nudged to change it.
var defaultNicknamePool = []string{
	"Alice", "Bob", "Carol", "Dave", "Eve", "Frank", "Grace", "Heidi",
	"Ivan", "Judy", "Karl", "Liam", "Mona", "Nina", "Oscar", "Peggy",
	"Quinn", "Rita", "Sam", "Trent", "Uma", "Victor", "Wendy", "Xena",
	"Yuki", "Zara", "Aria", "Bruno", "Cleo", "Dion", "Elsa", "Finn",
	"Gina", "Hugo", "Iris", "Jack", "Kira", "Leo", "Maya", "Noah",
}

// defaultNicknameFor derives a stable, reproducible default nickname from the
// user id — same id always yields the same name (no randomness, so it survives
// a re-create and is testable). Form: "hypi-Maya4821".
func defaultNicknameFor(userID int64) string {
	name := defaultNicknamePool[int(userID)%len(defaultNicknamePool)]
	// 4 digits derived from the id so two users sharing a pool name still differ.
	digits := 1000 + int((userID*2654435761)%9000)
	if digits < 0 {
		digits = -digits
	}
	return fmt.Sprintf("hypi-%s%d", name, digits)
}

// GetOrCreateProfile returns the user's profile, lazily creating it (with a
// generated default nickname) the first time it's read.
func (db *DB) GetOrCreateProfile(ctx context.Context, userID int64) (*UserProfile, error) {
	row := db.QueryRowContext(ctx, `SELECT `+profileCols+` FROM user_profiles WHERE user_id = ?`, userID)
	p, err := scanProfile(row)
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// Create on first touch.
	now := time.Now().Unix()
	nick := defaultNicknameFor(userID)
	if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO user_profiles
		(user_id, display_name, name_is_default, public_opt_in, lifetime_tokens, lifetime_requests, last_active_at, created_at, updated_at)
		VALUES (?, ?, 1, 0, 0, 0, 0, ?, ?)`, userID, nick, now, now); err != nil {
		return nil, err
	}
	row = db.QueryRowContext(ctx, `SELECT `+profileCols+` FROM user_profiles WHERE user_id = ?`, userID)
	return scanProfile(row)
}

// UpdateProfile applies a nickname and/or opt-in change. A nil pointer leaves
// that field untouched. Setting a non-empty displayName clears name_is_default.
func (db *DB) UpdateProfile(ctx context.Context, userID int64, displayName *string, publicOptIn *bool) (*UserProfile, error) {
	if _, err := db.GetOrCreateProfile(ctx, userID); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if displayName != nil {
		if _, err := db.ExecContext(ctx, `UPDATE user_profiles SET display_name=?, name_is_default=0, updated_at=? WHERE user_id=?`,
			*displayName, now, userID); err != nil {
			return nil, err
		}
	}
	if publicOptIn != nil {
		v := 0
		if *publicOptIn {
			v = 1
		}
		if _, err := db.ExecContext(ctx, `UPDATE user_profiles SET public_opt_in=?, updated_at=? WHERE user_id=?`,
			v, now, userID); err != nil {
			return nil, err
		}
	}
	return db.GetOrCreateProfile(ctx, userID)
}

// BumpActivity increments the running counters on a billed request. Best-effort
// and self-creating: if the profile row doesn't exist yet it is inserted with a
// generated default nickname so a brand-new user who fires traffic before ever
// opening the dashboard still lands on the leaderboard. Never returns an error
// to the hot path — failures are swallowed (the proxy must not be billed-path
// blocked on a counter write).
func (db *DB) BumpActivity(ctx context.Context, userID int64, tokens int64) {
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx, `UPDATE user_profiles
		SET lifetime_tokens = lifetime_tokens + ?, lifetime_requests = lifetime_requests + 1, last_active_at = ?, updated_at = ?
		WHERE user_id = ?`, tokens, now, now, userID)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return
	}
	// Row absent — create then re-apply. INSERT carries the first hit's counts.
	nick := defaultNicknameFor(userID)
	_, _ = db.ExecContext(ctx, `INSERT OR IGNORE INTO user_profiles
		(user_id, display_name, name_is_default, public_opt_in, lifetime_tokens, lifetime_requests, last_active_at, created_at, updated_at)
		VALUES (?, ?, 1, 0, ?, 1, ?, ?, ?)`, userID, nick, tokens, now, now, now)
}

// BumpInvites increments a user's confirmed-invite counter, used by the
// referral module so the existing leaderboard can also rank by invites.
// Self-creating + best-effort like BumpActivity: a brand-new inviter who has no
// profile row yet still gets one. Never returns an error to the caller.
func (db *DB) BumpInvites(ctx context.Context, userID int64) {
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx, `UPDATE user_profiles
		SET lifetime_invites = lifetime_invites + 1, updated_at = ?
		WHERE user_id = ?`, now, userID)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return
	}
	nick := defaultNicknameFor(userID)
	_, _ = db.ExecContext(ctx, `INSERT OR IGNORE INTO user_profiles
		(user_id, display_name, name_is_default, public_opt_in, lifetime_tokens, lifetime_requests, lifetime_invites, last_active_at, created_at, updated_at)
		VALUES (?, ?, 1, 0, 0, 0, 1, 0, ?, ?)`, userID, nick, now, now)
}

// LeaderboardRow is one ranked user. Identity (real name vs pseudonym) is
// resolved by the arena service at fan-out time, not here.
type LeaderboardRow struct {
	UserID           int64
	DisplayName      string
	PublicOptIn      bool
	LifetimeTokens   int64
	LifetimeRequests int64
	LifetimeInvites  int64
	LastActiveAt     time.Time
}

// LeaderboardMetric selects the sort column.
type LeaderboardMetric string

const (
	MetricTokens   LeaderboardMetric = "tokens"
	MetricRequests LeaderboardMetric = "requests"
	MetricInvites  LeaderboardMetric = "invites"
)

func (m LeaderboardMetric) column() string {
	switch m {
	case MetricRequests:
		return "lifetime_requests"
	case MetricInvites:
		return "lifetime_invites"
	default:
		return "lifetime_tokens"
	}
}

// Leaderboard returns the top `limit` users by the given metric, skipping
// disabled accounts. Only users who have actually transacted (a counter > 0)
// appear, so a fresh empty install shows an empty board rather than a wall of
// zero-activity rows.
func (db *DB) Leaderboard(ctx context.Context, metric LeaderboardMetric, limit int) ([]*LeaderboardRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	col := metric.column()
	rows, err := db.QueryContext(ctx, `SELECT p.user_id, p.display_name, p.public_opt_in, p.lifetime_tokens, p.lifetime_requests, p.lifetime_invites, p.last_active_at
		FROM user_profiles p JOIN users u ON u.id = p.user_id AND u.disabled = 0
		WHERE p.`+col+` > 0
		ORDER BY p.`+col+` DESC, p.last_active_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LeaderboardRow
	for rows.Next() {
		var r LeaderboardRow
		var optIn int
		var lastActive int64
		if err := rows.Scan(&r.UserID, &r.DisplayName, &optIn, &r.LifetimeTokens, &r.LifetimeRequests, &r.LifetimeInvites, &lastActive); err != nil {
			return nil, err
		}
		r.PublicOptIn = optIn != 0
		if lastActive > 0 {
			r.LastActiveAt = time.Unix(lastActive, 0)
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// RankOf returns the 1-based rank of a user by the given metric (number of
// users strictly ahead + 1). Returns 0 when the user has no activity yet.
func (db *DB) RankOf(ctx context.Context, userID int64, metric LeaderboardMetric) (int, error) {
	col := metric.column()
	var mine int64
	if err := db.QueryRowContext(ctx, `SELECT `+col+` FROM user_profiles WHERE user_id = ?`, userID).Scan(&mine); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	if mine <= 0 {
		return 0, nil
	}
	var ahead int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_profiles p JOIN users u ON u.id = p.user_id AND u.disabled = 0
		WHERE p.`+col+` > ?`, mine).Scan(&ahead); err != nil {
		return 0, err
	}
	return ahead + 1, nil
}
