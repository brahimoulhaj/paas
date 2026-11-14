package store

import (
	"context"
	"database/sql"
	"encoding/json"
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

// Phases of a running deployment, in the order they come. A Git build is building, an
// image pull is pulling; a Git rollback has neither and goes straight to starting.
// Draining is the old containers going after the switch: from switching on, the new
// container serves and the deployment can no longer be cancelled.
const (
	PhasePulling        = "pulling"
	PhaseBuilding       = "building"
	PhaseStarting       = "starting"
	PhaseHealthChecking = "health_checking"
	PhaseSwitching      = "switching"
	PhaseDraining       = "draining"
)

// PhaseEntry is when a deployment entered a phase.
type PhaseEntry struct {
	Phase string    `json:"phase"`
	At    time.Time `json:"at"`
}

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
	CommitMessage string // for a Git app: that commit's message, empty until it is fetched
	Port          int    // the app's port when this attempt was made
	ContainerID   string
	ContainerName string
	Error         string
	LogPath       string // relative to the data dir
	Attempts      int    // how many times it has started; more than 1 after a panel restart
	RollbackOf    string // a rollback: the deployment whose image it deploys again; "" otherwise
	Phase         string // the phase it is in, or ended in; "" before the first
	Phases        []PhaseEntry
	StartedAt     time.Time // zero until it starts
	FinishedAt    time.Time // zero until it ends
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const deploymentCols = `id, app_id, status, trigger, image, commit_sha, port, container_id, container_name, error, log_path,
	COALESCE(started_at, ''), COALESCE(finished_at, ''), created_at, updated_at, attempts, rollback_of, phase, phases, commit_message`

func scanDeployment(row scanner) (Deployment, error) {
	var d Deployment
	var started, finished, created, updated, phases string
	err := row.Scan(&d.ID, &d.AppID, &d.Status, &d.Trigger, &d.Image, &d.CommitSHA, &d.Port, &d.ContainerID, &d.ContainerName, &d.Error, &d.LogPath,
		&started, &finished, &created, &updated, &d.Attempts, &d.RollbackOf, &d.Phase, &phases, &d.CommitMessage)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, err
	}
	d.Phases = []PhaseEntry{}
	if phases != "" {
		if err := json.Unmarshal([]byte(phases), &d.Phases); err != nil {
			return Deployment{}, err
		}
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

// TriggerRollback is the trigger of a rollback deployment.
const TriggerRollback = "rollback"

// CreateRollbackDeployment records a queued rollback to src: the same image, commit and
// port, deployed again without a build.
func (s *Store) CreateRollbackDeployment(ctx context.Context, src Deployment, logPath func(deploymentID string) string) (Deployment, error) {
	did, ts := id.New(), now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO deployments (id, app_id, status, trigger, image, commit_sha, commit_message, port, log_path, rollback_of, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		did, src.AppID, DeployQueued, TriggerRollback, src.Image, src.CommitSHA, src.CommitMessage, src.Port, logPath(did), src.ID, ts, ts)
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
	return s.listDeployments(ctx, appID, limit, 0)
}

// ListDeploymentsPage returns one page of an app's deployments, newest first, skipping
// offset, and how many the app has in all.
func (s *Store) ListDeploymentsPage(ctx context.Context, appID string, limit, offset int) ([]Deployment, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE app_id = ?`, appID).Scan(&total); err != nil {
		return nil, 0, err
	}
	list, err := s.listDeployments(ctx, appID, limit, offset)
	return list, total, err
}

func (s *Store) listDeployments(ctx context.Context, appID string, limit, offset int) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deploymentCols+` FROM deployments WHERE app_id = ? ORDER BY id DESC LIMIT ? OFFSET ?`, appID, limit, offset)
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
		`UPDATE deployments SET status = ?, container_id = '', container_name = '', error = '', phase = '', phases = '[]', updated_at = ?
		 WHERE id = ? AND status IN (?, ?)`,
		DeployQueued, now(), deploymentID, DeployQueued, DeployRunning))
}

// EnterDeploymentPhase records that a running deployment has moved on to phase, and when.
func (s *Store) EnterDeploymentPhase(ctx context.Context, deploymentID, phase string) error {
	at, err := json.Marshal(time.Now().UTC().Truncate(time.Millisecond))
	if err != nil {
		return err
	}
	return affected(s.db.ExecContext(ctx,
		`UPDATE deployments SET phase = ?,
		   phases = json_insert(phases, '$[#]', json_object('phase', ?, 'at', json(?))),
		   updated_at = ?
		 WHERE id = ?`,
		phase, phase, string(at), now(), deploymentID))
}

// SetDeploymentCommit records the commit a Git deployment is building, and its message.
func (s *Store) SetDeploymentCommit(ctx context.Context, deploymentID, commitSHA, message string) error {
	return affected(s.db.ExecContext(ctx,
		`UPDATE deployments SET commit_sha = ?, commit_message = ?, updated_at = ? WHERE id = ?`, commitSHA, message, now(), deploymentID))
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

// FinishDeployment sets the final status; errMsg is empty on success. The status also
// closes the timeline: phases gets a last entry named after it, so the phase before it
// has an end. phase is left as it was: the phase the deployment ended in.
func (s *Store) FinishDeployment(ctx context.Context, deploymentID, status, errMsg string) error {
	at, err := json.Marshal(time.Now().UTC().Truncate(time.Millisecond))
	if err != nil {
		return err
	}
	ts := now()
	return affected(s.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, error = ?, finished_at = ?, updated_at = ?,
		   phases = json_insert(phases, '$[#]', json_object('phase', ?, 'at', json(?)))
		 WHERE id = ?`,
		status, errMsg, ts, ts, status, string(at), deploymentID))
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
