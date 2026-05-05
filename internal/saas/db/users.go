package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type User struct {
	ID            int64
	Email         string
	PWHash        string
	Role          string
	BalanceUSD    float64
	GroupID       int64
	EmailVerified bool
	Disabled      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
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
	if err := row.Scan(&u.ID, &u.Email, &u.PWHash, &u.Role, &u.BalanceUSD, &u.GroupID, &ev, &dis, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	u.EmailVerified = ev != 0
	u.Disabled = dis != 0
	u.CreatedAt = time.Unix(createdAt, 0)
	u.UpdatedAt = time.Unix(updatedAt, 0)
	return &u, nil
}

const userCols = `id, email, pw_hash, role, balance_usd, group_id, email_verified, disabled, created_at, updated_at`

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
	return db.GetUser(ctx, id)
}

func (db *DB) GetUser(ctx context.Context, id int64) (*User, error) {
	row := db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (db *DB) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	row := db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE email = ?`, email)
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
		where = " WHERE email LIKE ?"
		args = append(args, "%"+search+"%")
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, `SELECT `+userCols+` FROM users`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
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
