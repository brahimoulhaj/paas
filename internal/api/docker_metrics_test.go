package api

import (
	"testing"
	"time"

	"panel/internal/dockermetrics"
)

type fakeDockerMetrics struct{ snapshot dockermetrics.Snapshot }

func (m fakeDockerMetrics) Snapshot() dockermetrics.Snapshot { return m.snapshot }

func TestDockerMetricsAPI(t *testing.T) {
	e := newEnv(t)
	e.must(401, "GET", "/api/docker/metrics", nil)
	e.signIn()
	e.must(503, "GET", "/api/docker/metrics", nil)
	e.srv.SetDockerMetrics(fakeDockerMetrics{})
	e.must(503, "GET", "/api/docker/metrics", nil)
	now := time.Now().UTC()
	e.srv.SetDockerMetrics(fakeDockerMetrics{dockermetrics.Snapshot{CheckedAt: now, Online: false, Unavailable: []string{"daemon"}}})
	if m := e.must(200, "GET", "/api/docker/metrics", nil); m["online"] != false || m["containers"] != nil {
		t.Fatalf("offline metrics = %+v", m)
	}
	e.srv.SetDockerMetrics(fakeDockerMetrics{dockermetrics.Snapshot{
		CheckedAt: now, CollectedAt: &now, Online: true,
		Engine:     &dockermetrics.Engine{Version: "29.0.0", APIVersion: "1.52", CPUs: 8},
		Containers: &dockermetrics.Containers{Total: 5, Running: 3, Paused: 1, Stopped: 1}, Unavailable: []string{},
	}})
	m := e.must(200, "GET", "/api/docker/metrics", nil)
	if m["online"] != true || m["containers"].(map[string]any)["running"] != float64(3) || m["engine"].(map[string]any)["version"] != "29.0.0" {
		t.Fatalf("metrics = %+v", m)
	}
	res, err := e.client.Get(e.url + "/api/docker/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("Docker metrics can be cached")
	}
}
