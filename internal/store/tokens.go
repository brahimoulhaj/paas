package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"panel/internal/id"
)

// APIToken is a long-lived bearer credential. Only the token's hash is stored.
type APIToken struct {
	ID         string
	UserID     string
	Name       string
	LastUsedAt *time.Time
	CreatedAt  time.Time
}

func (s *Store) CreateAPIToken(ctx context.Context, userID, name, tokenHash string) (APIToken, error) {
	t := APIToken{ID: id.New(), UserID: userID, Name: name}
	ts := now()
	t.CreatedAt = parseTime(ts)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, token_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, userID, name, tokenHash, ts, ts)
	return t, err
}

// ListAPITokens returns a user's tokens, newest first.
func (s *Store) ListAPITokens(ctx context.Context, userID string) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, name, last_used_at, created_at FROM api_tokens WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteAPIToken revokes one of a user's tokens; ErrNotFound when the user has no such token.
func (s *Store) DeleteAPIToken(ctx context.Context, userID, tokenID string) (APIToken, error) {
	t, err := scanAPIToken(s.db.QueryRowContext(ctx,
		`DELETE FROM api_tokens WHERE id = ? AND user_id = ? RETURNING id, user_id, name, last_used_at, created_at`,
		tokenID, userID))
	return t, err
}

// APITokenUser returns the token with this hash and its owner, or ErrNotFound.
func (s *Store) APITokenUser(ctx context.Context, tokenHash string) (APIToken, User, error) {
	var t APIToken
	var lastUsed sql.NullString
	var tCreated, uCreated, uUpdated string
	var u User
	err := s.db.QueryRowContext(ctx, `
		SELECT t.id, t.user_id, t.name, t.last_used_at, t.created_at,
		       u.id, u.email, u.password_hash, u.role, u.created_at, u.updated_at
		FROM api_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ?`, tokenHash).Scan(
		&t.ID, &t.UserID, &t.Name, &lastUsed, &tCreated,
		&u.ID, &u.Email, &u.PasswordHash, &u.Role, &uCreated, &uUpdated)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, User{}, ErrNotFound
	}
	if err != nil {
		return APIToken{}, User{}, err
	}
	t.CreatedAt = parseTime(tCreated)
	if lastUsed.Valid {
		lu := parseTime(lastUsed.String)
		t.LastUsedAt = &lu
	}
	u.CreatedAt, u.UpdatedAt = parseTime(uCreated), parseTime(uUpdated)
	return t, u, nil
}

// TouchAPIToken records a use, at most once a minute per token so busy clients do not
// turn every request into a write.
func (s *Store) TouchAPIToken(ctx context.Context, tokenID string) error {
	ts := now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = ?, updated_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`,
		ts, ts, tokenID, fmtTime(time.Now().Add(-time.Minute)))
	return err
}

func scanAPIToken(row interface{ Scan(...any) error }) (APIToken, error) {
	var t APIToken
	var lastUsed sql.NullString
	var created string
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &lastUsed, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	if err != nil {
		return APIToken{}, err
	}
	t.CreatedAt = parseTime(created)
	if lastUsed.Valid {
		lu := parseTime(lastUsed.String)
		t.LastUsedAt = &lu
	}
	return t, nil
}
