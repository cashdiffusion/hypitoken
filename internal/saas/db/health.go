package db

import (
	"context"
	"time"
)

type ModelHealth struct {
	ID         int64
	AuthID     string
	Provider   string
	Model      string
	Status     string
	LatencyMs  int
	Error      string
	CheckedAt  time.Time
}

func (db *DB) UpsertModelHealth(ctx context.Context, h ModelHealth) error {
	now := time.Now().Unix()
	_, err := db.ExecContext(ctx, `INSERT INTO model_health (auth_id, provider, model, status, latency_ms, error, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(auth_id, model) DO UPDATE SET status=excluded.status, latency_ms=excluded.latency_ms, error=excluded.error, checked_at=excluded.checked_at, provider=excluded.provider`,
		h.AuthID, h.Provider, h.Model, h.Status, h.LatencyMs, h.Error, now)
	return err
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
