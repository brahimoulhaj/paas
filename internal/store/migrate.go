package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads files named "<version>_<name>.sql", sorted by version.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	seen := map[int]string{}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".sql")
		num, rest, ok := strings.Cut(name, "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v < 1 {
			return nil, fmt.Errorf("migration %q: name must be <version>_<name>.sql", e.Name())
		}
		if other, dup := seen[v]; dup {
			return nil, fmt.Errorf("migrations %q and %q share version %d", other, e.Name(), v)
		}
		seen[v] = e.Name()
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: rest, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// Migrate applies pending migrations, each in its own transaction. If the database
// already holds data, a consistent copy is written to the backup directory first.
// It returns the versions it applied.
func (s *Store) Migrate(ctx context.Context) ([]int, error) {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return nil, err
	}
	all, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	var pending []migration
	for _, m := range all {
		if m.version > current {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return nil, nil
	}
	if current > 0 {
		if err := s.backup(ctx, current); err != nil {
			return nil, fmt.Errorf("pre-migration backup: %w", err)
		}
	}
	var applied []int
	for _, m := range pending {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return applied, err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return applied, fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			m.version, m.name, now()); err != nil {
			tx.Rollback()
			return applied, err
		}
		if err := tx.Commit(); err != nil {
			return applied, err
		}
		applied = append(applied, m.version)
	}
	return applied, nil
}

// backup writes a consistent snapshot with VACUUM INTO, which is safe in WAL mode
// (copying the .db file alone would miss whatever still sits in the WAL).
func (s *Store) backup(ctx context.Context, version int) error {
	if s.backupDir == "" {
		return nil
	}
	if err := os.MkdirAll(s.backupDir, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("pre-migrate-v%d-%s.db", version, time.Now().UTC().Format("20060102T150405Z"))
	dest := filepath.Join(s.backupDir, name)
	_, err := s.db.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(dest, "'", "''")+"'")
	return err
}
