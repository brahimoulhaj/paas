package api

import (
	"context"
	"runtime"
	"testing"

	"panel/internal/store"
)

func TestBuildSettings(t *testing.T) {
	e := newEnv(t)
	e.srv.SetBuildDefaults(store.BuildLimits{CPUs: 1, MemoryMB: 1024})
	if code, _ := e.call("GET", "/api/settings/builds", nil); code != 401 {
		t.Fatalf("signed out GET = %d", code)
	}
	e.signIn()

	// Nothing saved: the environment's defaults.
	code, m := e.call("GET", "/api/settings/builds", nil)
	if code != 200 || m["cpus"] != 1.0 || m["memory_mb"] != 1024.0 || m["host_cpus"] != float64(runtime.NumCPU()) {
		t.Fatalf("defaults = %d %v", code, m)
	}

	for _, bad := range []map[string]any{
		{"cpus": -1, "memory_mb": 0},
		{"cpus": runtime.NumCPU() + 1, "memory_mb": 0},
		{"cpus": 0.05, "memory_mb": 0},
		{"cpus": 0, "memory_mb": 100},
		{"cpus": 0, "memory_mb": -5},
		{"cpus": 1, "memory_mb": 512, "extra": true},
	} {
		if code, m := e.call("PUT", "/api/settings/builds", bad); code != 422 && code != 400 {
			t.Errorf("PUT %v = %d %v", bad, code, m)
		}
	}

	// Saved values win over the defaults, including 0 (no cap).
	code, m = e.call("PUT", "/api/settings/builds", map[string]any{"cpus": 0.333, "memory_mb": 0})
	if code != 200 || m["cpus"] != 0.33 || m["memory_mb"] != 0.0 {
		t.Fatalf("PUT = %d %v", code, m)
	}
	l, err := e.srv.store.GetBuildLimits(context.Background(), store.BuildLimits{CPUs: 1, MemoryMB: 1024})
	if err != nil || l != (store.BuildLimits{CPUs: 0.33, MemoryMB: 0}) {
		t.Fatalf("stored = %v, %v", l, err)
	}
	if defaults, _ := m["defaults"].(map[string]any); defaults["memory_mb"] != 1024.0 {
		t.Errorf("defaults after PUT = %v", m["defaults"])
	}

	// Reset: back to the environment's defaults.
	code, m = e.call("DELETE", "/api/settings/builds", nil)
	if code != 200 || m["cpus"] != 1.0 || m["memory_mb"] != 1024.0 {
		t.Fatalf("reset = %d %v", code, m)
	}
}
