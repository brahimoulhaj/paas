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
	DeployCancelled = "cancelled"
)

// Finished reports whether the deployment has ended, whatever the outcome.
func (d Deployment) Finished() bool {
	return d.Status == DeploySucceeded || d.Status == DeployFailed || d.Status == DeployCancelled
}

type Deployment struct {
	ID            string
	AppID         string
	Status        string
	Trigger       string
	Image         string // for a Git app: the image built, empty until the build succeeds
	CommitSHA     string // for a Git app: the commit built, empty until it is fetched
	Port          int    // the app's port when this attempt was made
	ContainerID   string
	ContainerName string
	Error         string
	LogPath       string    // relative to the data dir
	Attempts      int       // how many times it has started; more than 1 after a panel restart
	StartedAt     time.Time // zero until it starts
	FinishedAt    time.Time // zero until it ends
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const deploymentCols = `id, app_id, status, trigger, image, commit_sha, port, container_id, container_name, error, log_path,
	COALESCE(started_at, ''), COALESCE(finished_at, ''), created_at, updated_at, attempts`

func scanDeployment(row scanner) (Deployment, error) {
	var d Deployment
	var started, finished, created, updated string
	err := row.Scan(&d.ID, &d.AppID, &d.Status, &d.Trigger, &d.Image, &d.CommitSHA, &d.Port, &d.ContainerID, &d.ContainerName, &d.Error, &d.LogPath,
		&started, &finished, &created, &updated, &d.Attempts)
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

// MarkDeploymentRunning records that an attempt has started, and counts it.
func (s *Store) MarkDeploymentRunning(ctx context.Context, deploymentID string) error {
	ts := now()
	return affected(s.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, started_at = ?, attempts = attempts + 1, updated_at = ? WHERE id = ?`,
		DeployRunning, ts, ts, deploymentID))
}

// RequeueDeployment puts an unfinished deployment back in the queue, to start again from
// the beginning: the container of the interrupted attempt is forgotten (the caller removes
// it). A finished deployment is left alone (ErrNotFound).
func (s *Store) RequeueDeployment(ctx context.Context, deploymentID string) error {
	return affected(s.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, container_id = '', container_name = '', error = '', updated_at = ?
		 WHERE id = ? AND status IN (?, ?)`,
		DeployQueued, now(), deploymentID, DeployQueued, DeployRunning))
}

// SetDeploymentCommit records the commit a Git deployment is building.
func (s *Store) SetDeploymentCommit(ctx context.Context, deploymentID, commitSHA string) error {
	return affected(s.db.ExecContext(ctx,
		`UPDATE deployments SET commit_sha = ?, updated_at = ? WHERE id = ?`, commitSHA, now(), deploymentID))
}

// SetDeploymentImage records the image a Git deployment built.
func (s *Store) SetDeploymentImage(ctx context.Context, deploymentID, image string) error {
	return affected(s.db.ExecContext(ctx,
		`UPDATE deployments SET image = ?, updated_at = ? WHERE id = ?`, image, now(), deploymentID))
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

// ListUnfinishedDeployments returns the deployments that are queued or running, oldest
// first. At startup these are the ones a previous panel process left behind.
func (s *Store) ListUnfinishedDeployments(ctx context.Context) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deploymentCols+` FROM deployments WHERE status IN (?, ?) ORDER BY id`, DeployQueued, DeployRunning)
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

// CountActiveDeployments counts deployments that are queued or running right now.
func (s *Store) CountActiveDeployments(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE status IN (?, ?)`, DeployQueued, DeployRunning).Scan(&n)
	return n, err
}
