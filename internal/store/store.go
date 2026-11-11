// Package store is the SQLite persistence layer: the panel's single source of truth.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrNotFound is returned when a single-row lookup matches nothing.
var ErrNotFound = errors.New("store: not found")

const timeLayout = "2006-01-02T15:04:05Z"

type Store struct {
	db        *sql.DB
	path      string
	backupDir string
}

// Open opens (creating if needed) the SQLite database at path in WAL mode.
// backupDir receives a copy of the database before pending migrations run.
func Open(path, backupDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	return &Store{db: db, path: path, backupDir: backupDir}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Ping reports whether the database answers.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func fmtTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(v string) time.Time {
	t, _ := time.Parse(timeLayout, v)
	return t
}

func now() string { return fmtTime(time.Now()) }
