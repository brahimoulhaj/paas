package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"

	"panel/internal/id"
)

// Setting keys.
const (
	SettingPanelDomain = "panel_domain"        // hostname the panel is served on over HTTPS
	SettingACMEEmail   = "acme_email"          // account email for the certificate authority
	SettingPanelIPOff  = "panel_ip_access_off" // "true": with a panel domain set, requests by IP (unknown hosts) are dropped
	SettingPanelTLS    = "panel_domain_tls"    // TLS mode of the panel domain: TLSAuto (default) or TLSHTTPOnly
	SettingOnboarded   = "onboarded_at"        // when the owner finished or skipped the onboarding steps
	SettingUpdateAuto  = "update_auto"         // "true": apply panel updates on their own
	SettingUpdateSeen  = "update_seen"         // finished_at of the last update result recorded as an event
	SettingBuildCPUs   = "build_cpus"          // CPU cap shared by Git builds; "0" = none; absent = the environment's default
	SettingBuildMemory = "build_memory_mb"     // memory cap (MB) shared by Git builds; "0" = none; absent = the environment's default
)

// BuildLimits caps the CPU and memory that Git builds share. Zero leaves a resource uncapped.
type BuildLimits struct {
	CPUs     float64 `json:"cpus"`
	MemoryMB int64   `json:"memory_mb"`
}

// GetBuildLimits returns the build limits saved in Settings; a limit never saved there
// takes its value from def (the environment).
func (s *Store) GetBuildLimits(ctx context.Context, def BuildLimits) (BuildLimits, error) {
	out := def
	if v, ok, err := s.GetSetting(ctx, SettingBuildCPUs); err != nil {
		return def, err
	} else if n, perr := strconv.ParseFloat(v, 64); ok && perr == nil && n >= 0 && !math.IsInf(n, 0) {
		out.CPUs = n
	}
	if v, ok, err := s.GetSetting(ctx, SettingBuildMemory); err != nil {
		return def, err
	} else if n, perr := strconv.ParseInt(v, 10, 64); ok && perr == nil && n >= 0 {
		out.MemoryMB = n
	}
	return out, nil
}

// SetBuildLimits saves both build limits, overriding the environment's defaults.
func (s *Store) SetBuildLimits(ctx context.Context, l BuildLimits) error {
	if err := s.SetSetting(ctx, SettingBuildCPUs, strconv.FormatFloat(l.CPUs, 'f', -1, 64)); err != nil {
		return err
	}
	return s.SetSetting(ctx, SettingBuildMemory, strconv.FormatInt(l.MemoryMB, 10))
}

// GetSetting returns the value and whether the key exists.
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	ts := now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (id, key, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		id.New(), key, value, ts, ts)
	return err
}

func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	return err
}
