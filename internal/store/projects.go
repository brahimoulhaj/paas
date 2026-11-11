package store

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"panel/internal/id"
)

// ErrConflict is returned when a write breaks a uniqueness rule or would orphan rows.
var ErrConflict = errors.New("store: conflict")

func conflict(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return err
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type scanner interface{ Scan(...any) error }

// ---- projects ----

type Project struct {
	ID          string
	Name        string
	Description string
	AppCount    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const projectSelect = `SELECT p.id, p.name, p.description,
	(SELECT COUNT(*) FROM apps a WHERE a.project_id = p.id), p.created_at, p.updated_at FROM projects p`

func scanProject(row scanner) (Project, error) {
	var p Project
	var created, updated string
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.AppCount, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	p.CreatedAt, p.UpdatedAt = parseTime(created), parseTime(updated)
	return p, err
}

func (s *Store) CreateProject(ctx context.Context, name, description string) (Project, error) {
	pid, ts := id.New(), now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		pid, name, description, ts, ts)
	if err != nil {
		return Project{}, conflict(err)
	}
	return s.GetProject(ctx, pid)
}

func (s *Store) GetProject(ctx context.Context, projectID string) (Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, projectSelect+` WHERE p.id = ?`, projectID))
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, projectSelect+` ORDER BY p.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdateProject(ctx context.Context, projectID, name, description string) (Project, error) {
	err := affected(s.db.ExecContext(ctx,
		`UPDATE projects SET name = ?, description = ?, updated_at = ? WHERE id = ?`, name, description, now(), projectID))
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, projectID)
}

// DeleteProject refuses with ErrConflict while the project still has apps.
func (s *Store) DeleteProject(ctx context.Context, projectID string) error {
	err := affected(s.db.ExecContext(ctx,
		`DELETE FROM projects WHERE id = ? AND NOT EXISTS (SELECT 1 FROM apps WHERE project_id = ?)`, projectID, projectID))
	if errors.Is(err, ErrNotFound) {
		if _, getErr := s.GetProject(ctx, projectID); getErr == nil {
			return ErrConflict // it exists, so it was the apps that stopped the delete
		}
	}
	return err
}

// ---- apps ----

const (
	StateRunning = "running"
	StateStopped = "stopped"
)

type App struct {
	ID                  string
	ProjectID           string
	Name                string
	SourceType          string
	Image               string
	Port                int
	CPULimit            float64
	MemoryMB            int64
	DesiredState        string
	CurrentDeploymentID string // empty until the first successful deploy
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

const appCols = `id, project_id, name, source_type, image, port, cpu_limit, memory_limit_mb,
	desired_state, COALESCE(current_deployment_id, ''), created_at, updated_at`

func scanApp(row scanner) (App, error) {
	var a App
	var created, updated string
	err := row.Scan(&a.ID, &a.ProjectID, &a.Name, &a.SourceType, &a.Image, &a.Port, &a.CPULimit, &a.MemoryMB,
		&a.DesiredState, &a.CurrentDeploymentID, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, ErrNotFound
	}
	a.CreatedAt, a.UpdatedAt = parseTime(created), parseTime(updated)
	return a, err
}

// AppInput holds the fields a user can set on an app.
type AppInput struct {
	Name     string
	Image    string
	Port     int
	CPULimit float64
	MemoryMB int64
}

func (s *Store) CreateApp(ctx context.Context, projectID string, in AppInput) (App, error) {
	aid, ts := id.New(), now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO apps (id, project_id, name, source_type, image, port, cpu_limit, memory_limit_mb, desired_state, created_at, updated_at)
		SELECT ?, id, ?, 'image', ?, ?, ?, ?, ?, ?, ? FROM projects WHERE id = ?`,
		aid, in.Name, in.Image, in.Port, in.CPULimit, in.MemoryMB, StateStopped, ts, ts, projectID)
	if err != nil {
		return App{}, conflict(err)
	}
	return s.GetApp(ctx, aid) // ErrNotFound when the project does not exist
}

func (s *Store) GetApp(ctx context.Context, appID string) (App, error) {
	return scanApp(s.db.QueryRowContext(ctx, `SELECT `+appCols+` FROM apps WHERE id = ?`, appID))
}

func (s *Store) AppExists(ctx context.Context, appID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps WHERE id = ?`, appID).Scan(&n)
	return n > 0, err
}

func (s *Store) ListApps(ctx context.Context, projectID string) ([]App, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+appCols+` FROM apps WHERE project_id = ? ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) UpdateApp(ctx context.Context, appID string, in AppInput) (App, error) {
	err := affected(s.db.ExecContext(ctx, `
		UPDATE apps SET name = ?, image = ?, port = ?, cpu_limit = ?, memory_limit_mb = ?, updated_at = ? WHERE id = ?`,
		in.Name, in.Image, in.Port, in.CPULimit, in.MemoryMB, now(), appID))
	if err != nil {
		return App{}, err
	}
	return s.GetApp(ctx, appID)
}

func (s *Store) SetAppDesiredState(ctx context.Context, appID, state string) error {
	return affected(s.db.ExecContext(ctx,
		`UPDATE apps SET desired_state = ?, updated_at = ? WHERE id = ?`, state, now(), appID))
}

// SetAppCurrentDeployment points the app at a deployment (empty clears it) and sets the
// desired state in the same statement, so the proxy never sees one without the other.
func (s *Store) SetAppCurrentDeployment(ctx context.Context, appID, deploymentID, state string) error {
	return affected(s.db.ExecContext(ctx,
		`UPDATE apps SET current_deployment_id = NULLIF(?, ''), desired_state = ?, updated_at = ? WHERE id = ?`,
		deploymentID, state, now(), appID))
}

// DeleteApp removes the app with its domains and deployments.
func (s *Store) DeleteApp(ctx context.Context, appID string) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM apps WHERE id = ?`, appID))
}

// ---- domains ----

type Domain struct {
	ID        string
	AppID     string
	Hostname  string
	TLSMode   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const domainCols = `id, app_id, hostname, tls_mode, created_at, updated_at`

func scanDomain(row scanner) (Domain, error) {
	var d Domain
	var created, updated string
	err := row.Scan(&d.ID, &d.AppID, &d.Hostname, &d.TLSMode, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Domain{}, ErrNotFound
	}
	d.CreatedAt, d.UpdatedAt = parseTime(created), parseTime(updated)
	return d, err
}

// TLS modes of a domain.
const (
	TLSAuto     = "auto"      // HTTPS with an automatic certificate; HTTP redirects to it
	TLSHTTPOnly = "http_only" // plain HTTP only, for a CDN that terminates TLS (Cloudflare Flexible)
)

// ValidTLSMode reports whether m is a TLS mode the proxy implements.
func ValidTLSMode(m string) bool { return m == TLSAuto || m == TLSHTTPOnly }

// CreateDomain returns ErrConflict when the hostname is already taken by any app.
func (s *Store) CreateDomain(ctx context.Context, appID, hostname, tlsMode string) (Domain, error) {
	if tlsMode == "" {
		tlsMode = TLSAuto
	}
	did, ts := id.New(), now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO domains (id, app_id, hostname, tls_mode, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		did, appID, hostname, tlsMode, ts, ts)
	if err != nil {
		return Domain{}, conflict(err)
	}
	return s.GetDomain(ctx, did)
}

func (s *Store) GetDomain(ctx context.Context, domainID string) (Domain, error) {
	return scanDomain(s.db.QueryRowContext(ctx, `SELECT `+domainCols+` FROM domains WHERE id = ?`, domainID))
}

func (s *Store) DomainExists(ctx context.Context, hostname string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domains WHERE hostname = ?`, hostname).Scan(&n)
	return n > 0, err
}

func (s *Store) ListDomains(ctx context.Context, appID string) ([]Domain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+domainCols+` FROM domains WHERE app_id = ? ORDER BY hostname`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) SetDomainTLSMode(ctx context.Context, domainID, tlsMode string) error {
	return affected(s.db.ExecContext(ctx, `UPDATE domains SET tls_mode = ?, updated_at = ? WHERE id = ?`, tlsMode, now(), domainID))
}

func (s *Store) DeleteDomain(ctx context.Context, domainID string) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM domains WHERE id = ?`, domainID))
}

// ProxyRoute is the routing state of one app that has domains.
type ProxyRoute struct {
	AppID    string
	Hosts    []string
	HTTPOnly []string // hosts served over plain HTTP only
	Upstream string   // container:port; empty when the app is stopped or was never deployed
}

// ProxyRoutes is what the proxy configuration is built from.
func (s *Store) ProxyRoutes(ctx context.Context) ([]ProxyRoute, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, COALESCE(d.port, 0), a.desired_state, COALESCE(d.container_name, ''), dm.hostname, dm.tls_mode
		FROM domains dm
		JOIN apps a ON a.id = dm.app_id
		LEFT JOIN deployments d ON d.id = a.current_deployment_id
		ORDER BY a.id, dm.hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProxyRoute
	for rows.Next() {
		var appID, state, container, host, mode string
		var port int
		if err := rows.Scan(&appID, &port, &state, &container, &host, &mode); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].AppID != appID {
			r := ProxyRoute{AppID: appID}
			if state == StateRunning && container != "" {
				r.Upstream = Upstream(container, port)
			}
			out = append(out, r)
		}
		out[len(out)-1].Hosts = append(out[len(out)-1].Hosts, host)
		if mode == TLSHTTPOnly {
			out[len(out)-1].HTTPOnly = append(out[len(out)-1].HTTPOnly, host)
		}
	}
	return out, rows.Err()
}

// Upstream is the address the proxy dials for a container on the shared network.
func Upstream(container string, port int) string {
	return net.JoinHostPort(container, strconv.Itoa(port))
}
