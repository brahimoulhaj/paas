package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"panel/internal/id"
)

const (
	DeployQueued    = "queued"
	DeployRunning   = "running"
	DeploySucceeded = "succeeded"
	DeployFailed    = "failed"
)

type Deployment struct {
	ID            string
	AppID         string
	Status        string
	Trigger       string
	Image         string
	Port          int // the app's port when this attempt was made
	ContainerID   string
	ContainerName string
	Error         string
	LogPath       string    // relative to the data dir
	StartedAt     time.Time // zero until it starts
	FinishedAt    time.Time // zero until it ends
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const deploymentCols = `id, app_id, status, trigger, image, port, container_id, container_name, error, log_path,
	COALESCE(started_at, ''), COALESCE(finished_at, ''), created_at, updated_at`

func scanDeployment(row scanner) (Deployment, error) {
	var d Deployment
	var started, finished, created, updated string
	err := row.Scan(&d.ID, &d.AppID, &d.Status, &d.Trigger, &d.Image, &d.Port, &d.ContainerID, &d.ContainerName, &d.Error, &d.LogPath,
		&started, &finished, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	d.StartedAt, d.FinishedAt = parseTime(started), parseTime(finished)
	d.CreatedAt, d.UpdatedAt = parseTime(created), parseTime(updated)
	return d, err
}

// CreateDeployment records a queued attempt. logPath is given the new deployment's id, so
// the log file can be named after it.
func (s *Store) CreateDeployment(ctx context.Context, appID, trigger, image string, port int, logPath func(deploymentID string) string) (Deployment, error) {
	did, ts := id.New(), now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO deployments (id, app_id, status, trigger, image, port, log_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		did, appID, DeployQueued, trigger, image, port, logPath(did), ts, ts)
	if err != nil {
		return Deployment{}, err
	}
	return s.GetDeployment(ctx, did)
}

func (s *Store) GetDeployment(ctx context.Context, deploymentID string) (Deployment, error) {
	return scanDeployment(s.db.QueryRowContext(ctx, `SELECT `+deploymentCols+` FROM deployments WHERE id = ?`, deploymentID))
}

// ListDeployments returns the newest first.
func (s *Store) ListDeployments(ctx context.Context, appID string, limit int) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deploymentCols+` FROM deployments WHERE app_id = ? ORDER BY id DESC LIMIT ?`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) MarkDeploymentRunning(ctx context.Context, deploymentID string) error {
	ts := now()
	return affected(s.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, started_at = ?, updated_at = ? WHERE id = ?`, DeployRunning, ts, ts, deploymentID))
}

func (s *Store) SetDeploymentContainer(ctx context.Context, deploymentID, containerID, containerName string) error {
	return affected(s.db.ExecContext(ctx,
		`UPDATE deployments SET container_id = ?, container_name = ?, updated_at = ? WHERE id = ?`,
		containerID, containerName, now(), deploymentID))
}

// FinishDeployment sets the final status; errMsg is empty on success.
func (s *Store) FinishDeployment(ctx context.Context, deploymentID, status, errMsg string) error {
	ts := now()
	return affected(s.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, error = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
		status, errMsg, ts, ts, deploymentID))
}

// FailUnfinishedDeployments closes attempts a previous panel process left open; nothing is
// running them any more. It returns how many it closed.
func (s *Store) FailUnfinishedDeployments(ctx context.Context, reason string) (int, error) {
	ts := now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, error = ?, finished_at = ?, updated_at = ? WHERE status IN (?, ?)`,
		DeployFailed, reason, ts, ts, DeployQueued, DeployRunning)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CountActiveDeployments counts deployments that are queued or running right now.
func (s *Store) CountActiveDeployments(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE status IN (?, ?)`, DeployQueued, DeployRunning).Scan(&n)
	return n, err
}
