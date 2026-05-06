package db

import (
	"context"
	"time"
)

type ModelHealth struct {
	ID        int64
	AuthID    string
	Provider  string
	Model     string
	Status    string
	LatencyMs int
	Error     string
	CheckedAt time.Time
}

func (db *DB) UpsertModelHealth(ctx context.Context, h ModelHealth) error {
	now := time.Now().Unix()
	_, err := db.ExecContext(ctx, `INSERT INTO model_health (auth_id, provider, model, status, latency_ms, error, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(auth_id, model) DO UPDATE SET status=excluded.status, latency_ms=excluded.latency_ms, error=excluded.error, checked_at=excluded.checked_at, provider=excluded.provider`,
		h.AuthID, h.Provider, h.Model, h.Status, h.LatencyMs, h.Error, now)
	return err
}

// AppendModelHealthHistory appends a probe result to the history table and
// prunes the per-(auth_id,model) history to the most recent 90 rows.
func (db *DB) AppendModelHealthHistory(ctx context.Context, h ModelHealth) error {
	now := time.Now().Unix()
	_, err := db.ExecContext(ctx,
		`INSERT INTO model_health_history (auth_id, provider, model, status, latency_ms, checked_at) VALUES (?,?,?,?,?,?)`,
		h.AuthID, h.Provider, h.Model, h.Status, h.LatencyMs, now)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`DELETE FROM model_health_history WHERE id IN (
			SELECT id FROM model_health_history
			WHERE auth_id=? AND model=?
			ORDER BY checked_at DESC
			LIMIT -1 OFFSET 90
		)`, h.AuthID, h.Model)
	return err
}

// ListModelHealthHistory returns the last `limit` probe results for a specific
// (auth_id, model) pair, ordered oldest-first so newest is last in the slice
// (left-to-right timeline).
func (db *DB) ListModelHealthHistory(ctx context.Context, authID, model string, limit int) ([]*ModelHealth, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, auth_id, provider, model, status, latency_ms, checked_at
		 FROM model_health_history
		 WHERE auth_id=? AND model=?
		 ORDER BY checked_at DESC
		 LIMIT ?`, authID, model, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ModelHealth
	for rows.Next() {
		var h ModelHealth
		var c int64
		if err := rows.Scan(&h.ID, &h.AuthID, &h.Provider, &h.Model, &h.Status, &h.LatencyMs, &c); err != nil {
			return nil, err
		}
		h.CheckedAt = time.Unix(c, 0)
		out = append(out, &h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse so oldest is first (left on the grid).
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// PruneModelHealthOtherModels deletes any health records (latest + history)
// for the given auth_id whose model is not in `keep`. Use this to drop stale
// rows left over after the probe model for a provider has been changed —
// without it the UNIQUE(auth_id, model) constraint preserves obsolete entries
// forever and the status page shows ghost credentials.
func (db *DB) PruneModelHealthOtherModels(ctx context.Context, authID string, keep []string) error {
	if authID == "" || len(keep) == 0 {
		return nil
	}
	q := `DELETE FROM model_health WHERE auth_id=? AND model NOT IN (?` + repeatPlaceholders(len(keep)-1) + `)`
	args := make([]any, 0, len(keep)+1)
	args = append(args, authID)
	for _, m := range keep {
		args = append(args, m)
	}
	if _, err := db.ExecContext(ctx, q, args...); err != nil {
		return err
	}
	q2 := `DELETE FROM model_health_history WHERE auth_id=? AND model NOT IN (?` + repeatPlaceholders(len(keep)-1) + `)`
	_, err := db.ExecContext(ctx, q2, args...)
	return err
}

func repeatPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, n*2)
	for range n {
		out = append(out, ',', '?')
	}
	return string(out)
}

func (db *DB) ListModelHealth(ctx context.Context) ([]*ModelHealth, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, auth_id, provider, model, status, latency_ms, error, checked_at FROM model_health ORDER BY auth_id, model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ModelHealth
	for rows.Next() {
		var h ModelHealth
		var c int64
		if err := rows.Scan(&h.ID, &h.AuthID, &h.Provider, &h.Model, &h.Status, &h.LatencyMs, &h.Error, &c); err != nil {
			return nil, err
		}
		h.CheckedAt = time.Unix(c, 0)
		out = append(out, &h)
	}
	return out, rows.Err()
}
