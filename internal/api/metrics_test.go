package api

import (
	"testing"
	"time"

	"panel/internal/hostmetrics"
)

type fakeHostMetrics struct{ snapshot hostmetrics.Snapshot }

func (m fakeHostMetrics) Snapshot() hostmetrics.Snapshot { return m.snapshot }

func TestHostMetricsAPI(t *testing.T) {
	e := newEnv(t)
	e.must(401, "GET", "/api/host/metrics", nil)
	e.signIn()
	e.must(503, "GET", "/api/host/metrics", nil)
	e.srv.SetHostMetrics(fakeHostMetrics{})
	e.must(503, "GET", "/api/host/metrics", nil)
	cpu := 35.5
	e.srv.SetHostMetrics(fakeHostMetrics{hostmetrics.Snapshot{
		CollectedAt: time.Now().UTC(), Hostname: "test-host", OS: "Linux", Arch: "arm64",
		CPU: hostmetrics.CPU{Cores: 8, Percent: &cpu}, Memory: &hostmetrics.Memory{Total: 1000, Available: 600, Used: 400, Percent: 40},
		History: []hostmetrics.Point{}, Unavailable: []string{"network"},
	}})
	m := e.must(200, "GET", "/api/host/metrics", nil)
	if m["hostname"] != "test-host" || m["cpu"].(map[string]any)["percent"] != cpu || m["network"] != nil || m["memory"].(map[string]any)["percent"] != float64(40) {
		t.Fatalf("metrics JSON = %+v", m)
	}
	res, err := e.client.Get(e.url + "/api/host/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("host metrics can be cached")
	}
}
