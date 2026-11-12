package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestPrimaryDomainLifecycle(t *testing.T) {
	s, ctx := open(t), context.Background()
	p, _ := s.CreateProject(ctx, "Shop", "")
	a, _ := s.CreateApp(ctx, p.ID, AppInput{Name: "web", Image: "nginx", Port: 80})
	b, _ := s.CreateApp(ctx, p.ID, AppInput{Name: "api", Image: "nginx", Port: 80})
	first, err := s.CreateDomain(ctx, a.ID, "z.example.com", "")
	if err != nil || !first.IsPrimary {
		t.Fatalf("first domain = %+v, %v", first, err)
	}
	second, err := s.CreateDomain(ctx, a.ID, "a.example.com", TLSHTTPOnly)
	if err != nil || second.IsPrimary {
		t.Fatalf("second domain = %+v, %v", second, err)
	}
	other, err := s.CreateDomain(ctx, b.ID, "api.example.com", "")
	if err != nil || !other.IsPrimary {
		t.Fatalf("other app's domain = %+v, %v", other, err)
	}
	assertPrimary := func(appID, want string) {
		t.Helper()
		list, err := s.ListDomains(ctx, appID)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, d := range list {
			if d.IsPrimary {
				count++
				if d.ID != want {
					t.Fatalf("primary = %s, want %s", d.ID, want)
				}
			}
		}
		if count != 1 || list[0].ID != want {
			t.Fatalf("expected one primary listed first: %+v", list)
		}
	}
	assertPrimary(a.ID, first.ID) // creation order, not alphabetical order
	updated, err := s.UpdateDomain(ctx, second.ID, "", true)
	if err != nil || !updated.IsPrimary || updated.TLSMode != TLSHTTPOnly {
		t.Fatalf("make primary = %+v, %v", updated, err)
	}
	assertPrimary(a.ID, second.ID)
	assertPrimary(b.ID, other.ID)
	if _, err := s.UpdateDomain(ctx, "missing", "", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing domain = %v", err)
	}
	assertPrimary(a.ID, second.ID)
	if _, err := s.UpdateDomain(ctx, first.ID, TLSAuto, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDomain(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	assertPrimary(a.ID, first.ID) // deleting a secondary keeps the choice
	second, _ = s.CreateDomain(ctx, a.ID, "a.example.com", "")
	third, _ := s.CreateDomain(ctx, a.ID, "b.example.com", "")
	if err := s.DeleteDomain(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	assertPrimary(a.ID, second.ID) // oldest remaining domain is promoted
	for _, d := range []Domain{second, third} {
		if err := s.DeleteDomain(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	if list, err := s.ListDomains(ctx, a.ID); err != nil || len(list) != 0 {
		t.Fatalf("delete all = %+v, %v", list, err)
	}
	first, err = s.CreateDomain(ctx, a.ID, "new.example.com", "")
	if err != nil || !first.IsPrimary {
		t.Fatalf("first after deleting all = %+v, %v", first, err)
	}
}

func TestPrimaryDomainConcurrentCreates(t *testing.T) {
	s, ctx := open(t), context.Background()
	p, _ := s.CreateProject(ctx, "Shop", "")
	a, _ := s.CreateApp(ctx, p.ID, AppInput{Name: "web", Image: "nginx", Port: 80})
	var wg sync.WaitGroup
	for _, host := range []string{"one.example.com", "two.example.com", "three.example.com"} {
		wg.Go(func() {
			if _, err := s.CreateDomain(ctx, a.ID, host, ""); err != nil {
				t.Errorf("create %s: %v", host, err)
			}
		})
	}
	wg.Wait()
	list, err := s.ListDomains(ctx, a.ID)
	if err != nil || len(list) != 3 {
		t.Fatalf("domains = %+v, %v", list, err)
	}
	count := 0
	for _, d := range list {
		if d.IsPrimary {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("concurrent creation produced %d primaries", count)
	}
}

func TestPrimaryDomainMigration(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "legacy.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	all, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	// Build the previous schema and seed domains before the primary column existed.
	for _, m := range all {
		if m.version >= 7 {
			break
		}
		if _, err := s.db.ExecContext(ctx, m.sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations VALUES (6, 'app_mounts', '2026-01-01T00:00:00Z');
		INSERT INTO projects (id, name, created_at, updated_at) VALUES ('p', 'Shop', '', '');
		INSERT INTO apps (id, project_id, name, image, port, created_at, updated_at)
		VALUES ('a', 'p', 'web', 'nginx', 80, '', ''), ('b', 'p', 'api', 'nginx', 80, '', '');
		INSERT INTO domains (id, app_id, hostname, created_at, updated_at) VALUES
		('002', 'a', 'a.example.com', '2026-01-01T00:00:00Z', ''),
		('001', 'a', 'z.example.com', '2026-01-01T00:00:00Z', ''),
		('003', 'b', 'api.example.com', '2026-01-01T00:00:00Z', '');`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for appID, primaryID := range map[string]string{"a": "001", "b": "003"} {
		list, err := s.ListDomains(ctx, appID)
		if err != nil || len(list) == 0 || list[0].ID != primaryID || !list[0].IsPrimary {
			t.Fatalf("migrated %s = %+v, %v", appID, list, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE domains SET is_primary = 1 WHERE id = '002'`); err == nil {
		t.Fatal("database allowed a second primary for the same app")
	}
}
