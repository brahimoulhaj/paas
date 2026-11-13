package store

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"panel/internal/secret"
)

func TestRegistries(t *testing.T) {
	s, ctx := open(t), context.Background()
	box, err := secret.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UseSecrets(ctx, box); err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRegistry(ctx, "GHCR", RegistryCredentials{Server: "ghcr.io", Username: "bot", Password: "ghp_s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.QueryRowContext(ctx, `SELECT secret FROM registries WHERE id = ?`, r.ID).Scan(&stored); err != nil || strings.Contains(stored, "s3cret") {
		t.Fatalf("password stored in clear: %q, %v", stored, err)
	}
	if c, err := s.RegistryCredentials(ctx, r.ID); err != nil || c != (RegistryCredentials{"ghcr.io", "bot", "ghp_s3cret"}) {
		t.Fatalf("credentials = %+v, %v", c, err)
	}
	if list, _ := s.ListRegistries(ctx); len(list) != 1 || list[0].Server != "ghcr.io" || list[0].Username != "bot" {
		t.Fatalf("list = %+v", list)
	}

	// An image app keeps its registry; a Git app never has one; a used registry stays.
	p, _ := s.CreateProject(ctx, "Shop", "")
	a, err := s.CreateApp(ctx, p.ID, AppInput{Name: "web", Image: "ghcr.io/org/web:1", RegistryID: r.ID, Port: 80})
	if err != nil || a.RegistryID != r.ID {
		t.Fatalf("app = %+v, %v", a, err)
	}
	if err := s.DeleteRegistry(ctx, r.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete used registry = %v", err)
	}
	a, err = s.UpdateApp(ctx, a.ID, AppInput{Name: "web", SourceType: SourceGit, RegistryID: r.ID, Port: 80,
		Git: GitSource{Repository: "https://example.com/a.git", Branch: "main", Builder: BuilderAuto, Dockerfile: "Dockerfile"}})
	if err != nil || a.RegistryID != "" {
		t.Fatalf("git app = %+v, %v", a, err)
	}
	if err := s.DeleteRegistry(ctx, r.ID); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if err := s.DeleteRegistry(ctx, r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete again = %v", err)
	}
}

func TestImageServer(t *testing.T) {
	for image, want := range map[string]string{
		"nginx":                                   DockerHub,
		"nginx:1.27":                              DockerHub,
		"org/app:1":                               DockerHub,
		"docker.io/org/app":                       DockerHub,
		"index.docker.io/org/app":                 DockerHub,
		"ghcr.io/org/app:1":                       "ghcr.io",
		"GHCR.io/org/app":                         "ghcr.io",
		"registry.example.com:5000/app":           "registry.example.com:5000",
		"localhost/app":                           "localhost",
		"localhost:5000/app@sha256:abc":           "localhost:5000",
		"123.dkr.ecr.eu-west-1.amazonaws.com/app": "123.dkr.ecr.eu-west-1.amazonaws.com",
	} {
		if got := ImageServer(image); got != want {
			t.Errorf("ImageServer(%q) = %q, want %q", image, got, want)
		}
	}
	for in, want := range map[string]string{"https://index.docker.io/v1/": DockerHub, "": DockerHub, "https://GHCR.io/": "ghcr.io", "registry.example.com:5000": "registry.example.com:5000"} {
		if got := NormalizeRegistryServer(in); got != want {
			t.Errorf("NormalizeRegistryServer(%q) = %q, want %q", in, got, want)
		}
	}
}
