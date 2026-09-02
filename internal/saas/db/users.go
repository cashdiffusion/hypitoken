package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

type User struct {
	ID            int64
	Email         string
	PWHash        string
	Role          string
	BalanceUSD    float64 // loaded from the personal workspace (see userFrom JOIN)
	GroupID       int64
	EmailVerified bool
	Disabled      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time

	PersonalWorkspaceID int64
}

const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

var ErrNotFound = errors.New("not found")

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var ev, dis int
	var createdAt, updatedAt int64
	var bal sql.NullFloat64
	if err := row.Scan(&u.ID, &u.Email, &u.PWHash, &u.Role, &bal, &u.GroupID, &ev, &dis, &createdAt, &updatedAt, &u.PersonalWorkspaceID); err != nil {
		return nil, err
	}
	if bal.Valid {
		u.BalanceUSD = bal.Float64
	} else {
		// No personal workspace behind this user. The old COALESCE quietly
		// served users.balance_usd here — a column frozen at the v13
		// migration, by now $8k adrift across 302 users — which is a
		// plausible-looking wrong number, the worst kind. $0 is at least
		// honest (nothing can be spent that is not there) and the log line is
		// the signal that a workspace pointer broke.
		log.Errorf("saas: user %d (personal_workspace_id=%d) has no workspace row — balance unavailable, reporting $0",
			u.ID, u.PersonalWorkspaceID)
	}
	u.EmailVerified = ev != 0
	u.Disabled = dis != 0
	u.CreatedAt = time.Unix(createdAt, 0)
	u.UpdatedAt = time.Unix(updatedAt, 0)
	return &u, nil
}

// userCols / userFrom load a user joined to their personal workspace so that
// User.BalanceUSD reflects the live wallet (balance moved to workspaces in v13;
// users.balance_usd is frozen and deliberately NOT read here — see scanUser
// for why a missing workspace reports $0 rather than the frozen value).
const userCols = `u.id, u.email, u.pw_hash, u.role, w.balance_usd, u.group_id, u.email_verified, u.disabled, u.created_at, u.updated_at, u.personal_workspace_id`

const userFrom = ` FROM users u LEFT JOIN workspaces w ON w.id = u.personal_workspace_id`

func (db *DB) CreateUser(ctx context.Context, email, pwHash, role string, groupID int64, emailVerified bool) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, errors.New("email required")
	}
	if role == "" {
		role = RoleUser
	}
	now := time.Now().Unix()
	ev := 0
	if emailVerified {
		ev = 1
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (email, pw_hash, role, balance_usd, group_id, email_verified, disabled, created_at, updated_at)
		 VALUES (?, ?, ?, 0, ?, ?, 0, ?, ?)`,
		email, pwHash, role, groupID, ev, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	// Establish the user's personal workspace (home wallet + billing subject)
	// so the "every user has a personal workspace" invariant holds for every
	// creation path (registration, admin bootstrap, tests). Idempotent.
	if _, err := db.CreatePersonalWorkspace(ctx, id, email, groupID); err != nil {
		return nil, err
	}
	return db.GetUser(ctx, id)
}

func (db *DB) GetUser(ctx context.Context, id int64) (*User, error) {
	row := db.QueryRowContext(ctx, `SELECT `+userCols+userFrom+` WHERE u.id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (db *DB) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	row := db.QueryRowContext(ctx, `SELECT `+userCols+userFrom+` WHERE u.email = ?`, email)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (db *DB) UpdateUserPassword(ctx context.Context, id int64, pwHash string) error {
	_, err := db.ExecContext(ctx, `UPDATE users SET pw_hash = ?, updated_at = ? WHERE id = ?`, pwHash, time.Now().Unix(), id)
	return err
}

func (db *DB) MarkEmailVerified(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `UPDATE users SET email_verified = 1, updated_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

func (db *DB) SetUserGroup(ctx context.Context, id, groupID int64) error {
	_, err := db.ExecContext(ctx, `UPDATE users SET group_id = ?, updated_at = ? WHERE id = ?`, groupID, time.Now().Unix(), id)
	return err
}

func (db *DB) SetUserRole(ctx context.Context, id int64, role string) error {
	_, err := db.ExecContext(ctx, `UPDATE users SET role = ?, updated_at = ? WHERE id = ?`, role, time.Now().Unix(), id)
	return err
}

func (db *DB) SetUserDisabled(ctx context.Context, id int64, disabled bool) error {
	d := 0
	if disabled {
		d = 1
	}
	_, err := db.ExecContext(ctx, `UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?`, d, time.Now().Unix(), id)
	return err
}

// ListUsers returns up to limit users, with optional email-substring search.
func (db *DB) ListUsers(ctx context.Context, search string, limit, offset int) ([]*User, int, error) {
	if limit <= 0 {
		limit = 50
	}
	args := []any{}
	where := ""
	search = strings.ToLower(strings.TrimSpace(search))
	if search != "" {
		where = " WHERE u.email LIKE ?"
		args = append(args, "%"+search+"%")
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users u`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `SELECT `+userCols+userFrom+where+` ORDER BY u.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

// EmailExists returns true if a user with this email already exists.
func (db *DB) EmailExists(ctx context.Context, email string) (bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email = ?`, email).Scan(&n)
	return n > 0, err
}

// CountUsers returns total user count (used for "is this the first user?" bootstrap logic).
func (db *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}
