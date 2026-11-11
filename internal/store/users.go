package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"panel/internal/id"
)

type User struct {
	ID           string
	Email        string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const userCols = `id, email, password_hash, role, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var created, updated string
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	u.CreatedAt, u.UpdatedAt = parseTime(created), parseTime(updated)
	return u, err
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, email, passwordHash, role string) (User, error) {
	u := User{ID: id.New(), Email: email, PasswordHash: passwordHash, Role: role}
	ts := now()
	u.CreatedAt, u.UpdatedAt = parseTime(ts), parseTime(ts)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (`+userCols+`) VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.PasswordHash, u.Role, ts, ts)
	return u, err
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE email = ?`, email))
}

func (s *Store) GetUserByID(ctx context.Context, userID string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, userID))
}

// FirstOwner returns the earliest-created owner.
func (s *Store) FirstOwner(ctx context.Context) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE role = 'owner' ORDER BY id LIMIT 1`))
}

func (s *Store) UpdateUserPassword(ctx context.Context, userID, passwordHash string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, passwordHash, now(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
