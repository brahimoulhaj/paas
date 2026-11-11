package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"), filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := open(t)
	applied, err := s.Migrate(context.Background())
	if err != nil || len(applied) != 0 {
		t.Fatalf("second migrate = %v, %v; want nothing applied", applied, err)
	}
}

func TestUsers(t *testing.T) {
	s, ctx := open(t), context.Background()
	if n, _ := s.CountUsers(ctx); n != 0 {
		t.Fatalf("count = %d", n)
	}
	u, err := s.CreateUser(ctx, "owner@example.com", "hash", "owner")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetUserByEmail(ctx, "OWNER@example.com") // case-insensitive
	if err != nil || got.ID != u.ID || got.CreatedAt.IsZero() {
		t.Fatalf("by email = %+v, %v", got, err)
	}
	if _, err := s.CreateUser(ctx, "Owner@Example.com", "hash", "owner"); err == nil {
		t.Fatal("duplicate email must fail")
	}
	if _, err := s.GetUserByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user err = %v", err)
	}
	if err := s.UpdateUserPassword(ctx, u.ID, "hash2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.FirstOwner(ctx); got.PasswordHash != "hash2" {
		t.Fatalf("password not updated: %+v", got)
	}
	if err := s.UpdateUserPassword(ctx, "missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing = %v", err)
	}
}

func TestSessions(t *testing.T) {
	s, ctx := open(t), context.Background()
	u, _ := s.CreateUser(ctx, "a@example.com", "h", "owner")
	if err := s.CreateSession(ctx, u.ID, "live", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, u.ID, "stale", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionUser(ctx, "live"); err != nil || got.ID != u.ID {
		t.Fatalf("live session = %+v, %v", got, err)
	}
	if _, err := s.SessionUser(ctx, "stale"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session err = %v", err)
	}
	if n, err := s.DeleteExpiredSessions(ctx); err != nil || n != 1 {
		t.Fatalf("expired removed = %d, %v", n, err)
	}
	if err := s.DeleteSession(ctx, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "live"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session err = %v", err)
	}
	if err := s.CreateSession(ctx, "no-such-user", "x", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("foreign keys must be enforced")
	}
}

func TestSettings(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, ok, err := s.GetSetting(ctx, "k"); ok || err != nil {
		t.Fatalf("missing = %v, %v", ok, err)
	}
	_ = s.SetSetting(ctx, "k", "1")
	_ = s.SetSetting(ctx, "k", "2") // upsert
	if v, ok, _ := s.GetSetting(ctx, "k"); !ok || v != "2" {
		t.Fatalf("value = %q, %v", v, ok)
	}
	_ = s.DeleteSetting(ctx, "k")
	if _, ok, _ := s.GetSetting(ctx, "k"); ok {
		t.Fatal("setting not deleted")
	}
}
