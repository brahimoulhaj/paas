package api

import (
	"context"
	"strings"
	"testing"

	"panel/internal/executor"
)

type fakeChecker struct{ calls []executor.RegistryAuth }

func (f *fakeChecker) RegistryLogin(_ context.Context, a executor.RegistryAuth) error {
	f.calls = append(f.calls, a)
	if a.Password != "good" {
		return executor.ErrUnauthorized
	}
	return nil
}

func TestRegistriesAPI(t *testing.T) {
	e := newEnv(t)
	check := &fakeChecker{}
	e.srv.SetRegistryChecker(check)
	if code, _ := e.call("GET", "/api/registries", nil); code != 401 {
		t.Fatalf("signed out = %d", code)
	}
	e.signIn()

	for _, bad := range []map[string]any{
		{"server": "https://ghcr.io/org", "username": "u", "password": "good"},
		{"server": "ghcr.io", "username": "", "password": "good"},
		{"server": "ghcr.io", "username": "u", "password": ""},
		{"server": "-x.io", "username": "u", "password": "good"},
	} {
		e.must(422, "POST", "/api/registries", bad)
	}
	e.must(422, "POST", "/api/registries", map[string]any{"server": "ghcr.io", "username": "u", "password": "bad"})
	reg := e.must(201, "POST", "/api/registries", map[string]any{"server": "https://GHCR.io/", "username": "bot", "password": "good"})
	if reg["server"] != "ghcr.io" || reg["name"] != "ghcr.io" || reg["username"] != "bot" || reg["password"] != nil {
		t.Fatalf("registry = %v", reg)
	}
	if last := check.calls[len(check.calls)-1]; last.Server != "ghcr.io" {
		t.Errorf("login checked against %q", last.Server)
	}
	hub := e.must(201, "POST", "/api/registries", map[string]any{"name": "Hub", "server": "", "username": "me", "password": "good"})
	if hub["server"] != "docker.io" {
		t.Errorf("hub = %v", hub)
	}
	if list := e.list("/api/registries"); len(list) != 2 {
		t.Fatalf("list = %v", list)
	}

	// Image apps name a registry; the image must be on its host.
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "Shop"})["id"].(string)
	app := map[string]any{"name": "web", "image": "ghcr.io/org/web:1", "port": 80, "registry_id": reg["id"]}
	a := e.must(201, "POST", "/api/projects/"+pid+"/apps", app)
	if a["registry_id"] != reg["id"] {
		t.Fatalf("app = %v", a)
	}
	e.must(422, "PATCH", "/api/apps/"+a["id"].(string), map[string]any{"image": "nginx:1"}) // Docker Hub image, ghcr.io registry
	e.must(422, "PATCH", "/api/apps/"+a["id"].(string), map[string]any{"registry_id": "nope"})
	if got := e.must(200, "PATCH", "/api/apps/"+a["id"].(string), map[string]any{"image": "nginx:1", "registry_id": hub["id"]}); got["registry_id"] != hub["id"] {
		t.Errorf("switch to hub = %v", got)
	}
	// Saving other fields keeps the registry.
	if got := e.must(200, "PATCH", "/api/apps/"+a["id"].(string), map[string]any{"environment": map[string]string{"A": "1"}}); got["registry_id"] != hub["id"] {
		t.Errorf("registry lost on an environment save: %v", got)
	}

	e.must(409, "DELETE", "/api/registries/"+hub["id"].(string), nil)
	e.must(204, "DELETE", "/api/registries/"+reg["id"].(string), nil)
	e.must(404, "DELETE", "/api/registries/"+reg["id"].(string), nil)
	events := e.list("/api/events")
	found := false
	for _, ev := range events {
		if ev["action"] == "registry.add" && !strings.Contains(ev["detail"].(string), "good") {
			found = true
		}
	}
	if !found {
		t.Errorf("no registry.add event without the password: %v", events)
	}
}
