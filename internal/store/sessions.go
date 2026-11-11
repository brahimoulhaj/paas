package store

import (
	"context"
	"time"

	"panel/internal/id"
)

func (s *Store) CreateSession(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	ts := now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, token_hash, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id.New(), userID, tokenHash, fmtTime(expiresAt), ts, ts)
	return err
}

// SessionUser returns the user owning an unexpired session, or ErrNotFound.
func (s *Store) SessionUser(ctx context.Context, tokenHash string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.password_hash, u.role, u.created_at, u.updated_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, now()))
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteUserSessions signs a user out everywhere (used after a password reset).
func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
