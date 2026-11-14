package api

import (
	"context"
	"testing"

	"panel/internal/store"
)

func TestHealthCheckAPI(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "Shop"})["id"].(string)
	id := e.must(201, "POST", "/api/projects/"+pid+"/apps", map[string]any{"name": "web", "image": "nginx:1", "port": 80})["id"].(string)

	// A new app gets the TCP check.
	h := e.must(200, "GET", "/api/apps/"+id+"/health-check", nil)
	if h["type"] != "tcp" || h["saved"] != true || h["start_period"] != float64(30) {
		t.Fatalf("new app check = %v", h)
	}
	// An app from before health checks has none saved: the short wait.
	old, err := e.srv.store.CreateApp(context.Background(), pid, store.AppInput{Name: "old", Image: "nginx:1", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	if h := e.must(200, "GET", "/api/apps/"+old.ID+"/health-check", nil); h["type"] != "running" || h["saved"] != false {
		t.Fatalf("old app check = %v", h)
	}

	ok := map[string]any{"type": "http", "path": " /healthz ", "expected_status": 204, "interval": 5, "timeout": 2, "retries": 4, "start_period": 60}
	if h := e.must(200, "PUT", "/api/apps/"+id+"/health-check", ok); h["path"] != "/healthz" || h["saved"] != true {
		t.Fatalf("saved = %v", h)
	}
	if h := e.must(200, "GET", "/api/apps/"+id+"/health-check", nil); h["type"] != "http" || h["expected_status"] != float64(204) || h["retries"] != float64(4) {
		t.Fatalf("read back = %v", h)
	}

	bad := []map[string]any{
		{"type": "udp", "interval": 1, "timeout": 1, "retries": 1},
		{"type": "http", "path": "healthz", "interval": 1, "timeout": 1, "retries": 1},
		{"type": "http", "path": "/a b", "interval": 1, "timeout": 1, "retries": 1},
		{"type": "http", "path": "/", "expected_status": 99, "interval": 1, "timeout": 1, "retries": 1},
		{"type": "tcp", "interval": 0, "timeout": 1, "retries": 1},
		{"type": "tcp", "interval": 1, "timeout": 61, "retries": 1},
		{"type": "tcp", "interval": 1, "timeout": 1, "retries": 0},
		{"type": "tcp", "interval": 1, "timeout": 1, "retries": 1, "start_period": 901},
		{"type": "running", "start_period": 0},
	}
	for _, b := range bad {
		if code, _ := e.call("PUT", "/api/apps/"+id+"/health-check", b); code != 400 {
			t.Errorf("%v: %d, want 400", b, code)
		}
	}
	if code, _ := e.call("PUT", "/api/apps/nope/health-check", ok); code != 404 {
		t.Errorf("unknown app: %d", code)
	}
	if code, _ := e.call("GET", "/api/apps/nope/health-check", nil); code != 404 {
		t.Errorf("unknown app: %d", code)
	}

	// A running check needs only the time to stay up.
	e.must(200, "PUT", "/api/apps/"+id+"/health-check", map[string]any{"type": "running", "start_period": 10})
}
