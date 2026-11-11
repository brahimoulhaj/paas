package store

import (
	"context"
	"database/sql"
	"errors"

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
)

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
