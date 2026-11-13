package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"panel/internal/id"
	"panel/internal/secret"
)

// DockerHub is the server name of Docker Hub, where images without a registry host live.
const DockerHub = "docker.io"

// Registry is a private image registry the panel pulls from, without its password.
type Registry struct {
	ID        string
	Name      string
	Server    string // host[:port]; DockerHub for Docker Hub
	Username  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RegistryCredentials is what a pull or a login needs.
type RegistryCredentials struct {
	Server   string
	Username string
	Password string
}

const registryCols = `id, name, server, username, created_at, updated_at`

func scanRegistry(row scanner) (Registry, error) {
	var r Registry
	var created, updated string
	err := row.Scan(&r.ID, &r.Name, &r.Server, &r.Username, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Registry{}, ErrNotFound
	}
	r.CreatedAt, r.UpdatedAt = parseTime(created), parseTime(updated)
	return r, err
}

// CreateRegistry stores a registry; the password is encrypted with the panel key.
func (s *Store) CreateRegistry(ctx context.Context, name string, c RegistryCredentials) (Registry, error) {
	sealed := c.Password
	if s.box != nil {
		var err error
		if sealed, err = s.box.Encrypt([]byte(c.Password)); err != nil {
			return Registry{}, err
		}
	}
	rid, ts := id.New(), now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO registries (id, name, server, username, secret, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rid, name, c.Server, c.Username, sealed, ts, ts)
	if err != nil {
		return Registry{}, conflict(err)
	}
	return s.GetRegistry(ctx, rid)
}

func (s *Store) GetRegistry(ctx context.Context, registryID string) (Registry, error) {
	return scanRegistry(s.db.QueryRowContext(ctx, `SELECT `+registryCols+` FROM registries WHERE id = ?`, registryID))
}

func (s *Store) ListRegistries(ctx context.Context) ([]Registry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+registryCols+` FROM registries ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Registry{}
	for rows.Next() {
		r, err := scanRegistry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RegistryCredentials returns a registry's server, username and decrypted password.
func (s *Store) RegistryCredentials(ctx context.Context, registryID string) (RegistryCredentials, error) {
	var c RegistryCredentials
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT server, username, secret FROM registries WHERE id = ?`, registryID).
		Scan(&c.Server, &c.Username, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	c.Password = stored
	if secret.IsSealed(stored) {
		if s.box == nil {
			return c, errors.New("registry password is encrypted, and the store has no secret key")
		}
		plain, err := s.box.Decrypt(stored)
		if err != nil {
			return c, err
		}
		c.Password = string(plain)
	}
	return c, nil
}

// DeleteRegistry refuses with ErrConflict while an app still pulls from the registry.
func (s *Store) DeleteRegistry(ctx context.Context, registryID string) error {
	err := affected(s.db.ExecContext(ctx,
		`DELETE FROM registries WHERE id = ? AND NOT EXISTS (SELECT 1 FROM apps WHERE registry_id = ?)`, registryID, registryID))
	if errors.Is(err, ErrNotFound) {
		if _, getErr := s.GetRegistry(ctx, registryID); getErr == nil {
			return ErrConflict
		}
	}
	return err
}

// ImageServer is the registry host an image reference pulls from, normalized like
// NormalizeRegistryServer: "nginx" and "org/app" are on Docker Hub; "ghcr.io/org/app" on
// ghcr.io. Docker reads the first path part as a host when it has a dot or a colon, or is
// localhost.
func ImageServer(image string) string {
	first, _, found := strings.Cut(image, "/")
	if !found || (!strings.ContainsAny(first, ".:") && first != "localhost") {
		return DockerHub
	}
	return NormalizeRegistryServer(first)
}

// NormalizeRegistryServer lowercases a registry host and maps Docker Hub's names to
// DockerHub. A scheme or trailing slash someone pasted is dropped.
func NormalizeRegistryServer(server string) string {
	server = strings.ToLower(strings.TrimSpace(server))
	server = strings.TrimPrefix(strings.TrimPrefix(server, "https://"), "http://")
	server = strings.TrimSuffix(server, "/")
	server = strings.TrimSuffix(server, "/v1")
	server = strings.TrimSuffix(server, "/v2")
	switch server {
	case "", "docker.io", "index.docker.io", "registry-1.docker.io", "hub.docker.com":
		return DockerHub
	}
	return server
}
