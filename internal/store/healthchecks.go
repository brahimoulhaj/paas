package store

import (
	"context"
	"database/sql"
	"errors"
)

// Health check types.
const (
	HealthTCP     = "tcp"     // the app's port accepts a connection
	HealthHTTP    = "http"    // a GET on Path answers ExpectedStatus (0: any 2xx or 3xx)
	HealthRunning = "running" // the container stays up for StartPeriod seconds
)

// HealthCheck is how a deploy decides the new container may take traffic. The panel runs
// it against the container on the shared Docker network, before switching the route.
type HealthCheck struct {
	Type           string `json:"type"`
	Path           string `json:"path"`
	ExpectedStatus int    `json:"expected_status"`
	Interval       int    `json:"interval"`     // seconds between attempts
	Timeout        int    `json:"timeout"`      // seconds one attempt may take
	Retries        int    `json:"retries"`      // failures in a row, after the start period, that fail the deploy
	StartPeriod    int    `json:"start_period"` // seconds when failures do not count; running: how long to stay up
}

// DefaultHealthCheck is what new apps get: their port must accept a connection.
func DefaultHealthCheck() HealthCheck {
	return HealthCheck{Type: HealthTCP, Path: "/", Interval: 2, Timeout: 5, Retries: 3, StartPeriod: 30}
}

// LegacyHealthCheck is what an app with no health check saved gets, the behaviour from
// before health checks existed: its container must stay up for 3 seconds.
func LegacyHealthCheck() HealthCheck {
	return HealthCheck{Type: HealthRunning, Path: "/", Interval: 2, Timeout: 5, Retries: 3, StartPeriod: 3}
}

// AppHealthCheck returns the app's health check, and whether one was saved (false: the
// legacy one is returned).
func (s *Store) AppHealthCheck(ctx context.Context, appID string) (HealthCheck, bool, error) {
	var h HealthCheck
	err := s.db.QueryRowContext(ctx, `SELECT type, path, expected_status, interval_s, timeout_s, retries, start_period_s
		FROM app_health_checks WHERE app_id = ?`, appID).
		Scan(&h.Type, &h.Path, &h.ExpectedStatus, &h.Interval, &h.Timeout, &h.Retries, &h.StartPeriod)
	if errors.Is(err, sql.ErrNoRows) {
		if ok, err := s.AppExists(ctx, appID); err != nil || !ok {
			if err == nil {
				err = ErrNotFound
			}
			return HealthCheck{}, false, err
		}
		return LegacyHealthCheck(), false, nil
	}
	return h, err == nil, err
}

// SetAppHealthCheck saves the app's health check; the caller validates it.
func (s *Store) SetAppHealthCheck(ctx context.Context, appID string, h HealthCheck) error {
	return affected(s.db.ExecContext(ctx, `
		INSERT INTO app_health_checks (app_id, type, path, expected_status, interval_s, timeout_s, retries, start_period_s, updated_at)
		SELECT id, ?, ?, ?, ?, ?, ?, ?, ? FROM apps WHERE id = ?
		ON CONFLICT (app_id) DO UPDATE SET type = excluded.type, path = excluded.path,
			expected_status = excluded.expected_status, interval_s = excluded.interval_s, timeout_s = excluded.timeout_s,
			retries = excluded.retries, start_period_s = excluded.start_period_s, updated_at = excluded.updated_at`,
		h.Type, h.Path, h.ExpectedStatus, h.Interval, h.Timeout, h.Retries, h.StartPeriod, now(), appID))
}
