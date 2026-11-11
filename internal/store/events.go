package store

import (
	"context"
	"time"

	"panel/internal/id"
)

// Event is one entry of the audit log.
type Event struct {
	ID         string
	UserID     string // empty for system events
	Actor      string // email at the time, or "system"
	Action     string // e.g. "app.create"
	TargetType string
	TargetID   string
	TargetName string
	Detail     string
	CreatedAt  time.Time
}

func (s *Store) AddEvent(ctx context.Context, e Event) error {
	ts := now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO events (id, user_id, actor, action, target_type, target_id, target_name, detail, created_at, updated_at)
		VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?)`,
		id.New(), e.UserID, e.Actor, e.Action, e.TargetType, e.TargetID, e.TargetName, e.Detail, ts, ts)
	return err
}

// ListEvents returns the newest first.
func (s *Store) ListEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(user_id, ''), actor, action, target_type, target_id, target_name, detail, created_at
		FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var created string
		if err := rows.Scan(&e.ID, &e.UserID, &e.Actor, &e.Action, &e.TargetType, &e.TargetID, &e.TargetName, &e.Detail, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}
