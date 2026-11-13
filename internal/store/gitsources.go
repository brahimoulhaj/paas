package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"panel/internal/id"
)

// Types of a Git connection.
const (
	GitTypeGitHubApp = "github_app"
	GitTypeGitLab    = "gitlab"
	GitTypeDeployKey = "deploy_key" // an SSH key pair; clones any host over SSH
)

// GitConnection is a connection to a Git host (a row of git_sources). Secret is
// encrypted; internal/gitsource reads and writes it.
type GitConnection struct {
	ID        string
	Type      string
	Name      string
	BaseURL   string
	Secret    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const gitConnCols = `id, type, name, base_url, secret, created_at, updated_at`

func scanGitConn(row scanner) (GitConnection, error) {
	var c GitConnection
	var created, updated string
	err := row.Scan(&c.ID, &c.Type, &c.Name, &c.BaseURL, &c.Secret, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return GitConnection{}, ErrNotFound
	}
	c.CreatedAt, c.UpdatedAt = parseTime(created), parseTime(updated)
	return c, err
}

func (s *Store) CreateGitConnection(ctx context.Context, typ, name, baseURL, secret string) (GitConnection, error) {
	cid, ts := id.New(), now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO git_sources (id, type, name, base_url, secret, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cid, typ, name, baseURL, secret, ts, ts)
	if err != nil {
		return GitConnection{}, err
	}
	return s.GetGitConnection(ctx, cid)
}

func (s *Store) GetGitConnection(ctx context.Context, connID string) (GitConnection, error) {
	return scanGitConn(s.db.QueryRowContext(ctx, `SELECT `+gitConnCols+` FROM git_sources WHERE id = ?`, connID))
}

func (s *Store) ListGitConnections(ctx context.Context) ([]GitConnection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+gitConnCols+` FROM git_sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GitConnection{}
	for rows.Next() {
		c, err := scanGitConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteGitConnection refuses with ErrConflict while an app still uses the connection.
func (s *Store) DeleteGitConnection(ctx context.Context, connID string) error {
	err := affected(s.db.ExecContext(ctx,
		`DELETE FROM git_sources WHERE id = ? AND NOT EXISTS (SELECT 1 FROM apps WHERE git_source_id = ?)`, connID, connID))
	if errors.Is(err, ErrNotFound) {
		if _, getErr := s.GetGitConnection(ctx, connID); getErr == nil {
			return ErrConflict
		}
	}
	return err
}
