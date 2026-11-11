package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"panel/internal/executor"
)

const (
	caddyImage   = "caddy:2.10.2-alpine"
	backendImage = "busybox:1.37"
)

type stack struct {
	t         *testing.T
	ex        *executor.Docker
	name      string
	dir       string
	httpPort  int
	httpsPort int

	mu  sync.Mutex
	cfg Config
}

func (s *stack) source(context.Context) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg, nil
}

func (s *stack) set(c Config) {
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// newStack starts three tiny HTTP backends (answering "panel", "A" and "B") on a private
// network. Skipped without Docker.
func newStack(t *testing.T) *stack {
	t.Helper()
	if testing.Short() {
		t.Skip("needs Docker; skipped with -short")
	}
	name := fmt.Sprintf("pxytest-%d", time.Now().UnixNano())
	ex, err := executor.NewDocker(executor.Options{Network: name, LabelPrefix: name})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := ex.Ping(ctx); err != nil {
		ex.Close()
		t.Skipf("no Docker daemon: %v", err)
	}
	dir, err := os.MkdirTemp("", "pxytest")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	s := &stack{t: t, ex: ex, name: name, dir: dir, httpPort: freePort(t), httpsPort: freePort(t)}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		list, _ := ex.ListContainers(ctx)
		for _, c := range list {
			_ = ex.RemoveContainer(ctx, c.ID, true)
		}
		_ = ex.RemoveNetwork(ctx)
		ex.Close()
		os.RemoveAll(dir)
	})

	if err := ex.EnsureNetwork(ctx); err != nil {
		t.Fatal(err)
	}
	for _, img := range []string{backendImage, caddyImage} {
		if err := ex.PullImage(ctx, img, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range []string{"panel", "A", "B"} {
		id, err := ex.CreateContainer(ctx, executor.ContainerSpec{
			Name:       s.backend(b),
			Image:      backendImage,
			Kind:       executor.KindApp,
			ResourceID: b,
			Command:    []string{"sh", "-c", "mkdir -p /www && printf %s " + b + " > /www/index.html && exec httpd -f -p 8080 -h /www"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := ex.StartContainer(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func (s *stack) backend(b string) string  { return strings.ToLower(s.name + "-" + b) }
func (s *stack) upstream(b string) string { return s.backend(b) + ":8080" }

func (s *stack) manager(httpPort int) *Manager {
	s.t.Helper()
	m, err := New(Options{
		Executor:      s.ex,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:          s.name + "-proxy",
		Image:         caddyImage,
		Dir:           s.dir,
		HTTPPort:      httpPort,
		HTTPSPort:     s.httpsPort,
		Admin:         AdminTCP,
		AdminPort:     freePort(s.t),
		PanelUpstream: s.upstream("panel"),
		Source:        s.source,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return m
}

// get requests host through the proxy; https picks the port and sets SNI.
func (s *stack) get(httpPort int, https bool, host string) (int, string, string) {
	client := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, ServerName: host}, // internal test CA
			DisableKeepAlives: true,
		},
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/hello?x=1", httpPort)
	if https {
		url = fmt.Sprintf("https://127.0.0.1:%d/index.html", s.httpsPort)
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Host = host
	res, err := client.Do(req)
	if err != nil {
		return 0, err.Error(), ""
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body), res.Header.Get("Location")
}

// eventually retries: certificates and container starts take a moment.
func (s *stack) eventually(what string, check func() (bool, string)) {
	s.t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for {
		ok, detail := check()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("%s: %s", what, detail)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func (s *stack) expectHTTPS(host, want string) {
	s.t.Helper()
	s.eventually("https "+host+" -> "+want, func() (bool, string) {
		code, body, _ := s.get(0, true, host)
		return code == 200 && body == want, fmt.Sprintf("got %d %q", code, body)
	})
}

func TestManagerWithRealCaddy(t *testing.T) {
	s := newStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	m := s.manager(s.httpPort)

	// Fresh install: no hostnames; any plain-HTTP request reaches the panel.
	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.eventually("http catch-all -> panel", func() (bool, string) {
		code, _, _ := s.get(s.httpPort, false, "203.0.113.10")
		return code == 404, fmt.Sprintf("got %d", code) // busybox httpd has no /hello: a 404 from the panel backend
	})
	if code, body, _ := s.get(s.httpPort, false, "203.0.113.10"); code != 404 || !strings.Contains(body, "404") {
		t.Fatalf("catch-all = %d %q", code, body)
	}

	// Start again on a healthy proxy: same container, no recreate.
	before, err := s.ex.InspectContainer(ctx, s.name+"-proxy")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	after, _ := s.ex.InspectContainer(ctx, s.name+"-proxy")
	if after.ID != before.ID || !after.StartedAt.Equal(before.StartedAt) {
		t.Error("a healthy proxy was recreated or restarted")
	}
	if !IsProxy(after.Kind, after.ResourceID) {
		t.Errorf("proxy labels: kind=%q resource=%q", after.Kind, after.ResourceID)
	}

	// Panel domain and an app route, with certificates from the internal authority.
	s.set(Config{
		PanelDomain: "panel.test",
		Routes:      []Route{{ID: "app1", Hosts: []string{"app.test", "www.app.test"}, Upstreams: []string{s.upstream("A")}}},
	})
	if err := m.Apply(ctx); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	s.expectHTTPS("app.test", "A")
	s.expectHTTPS("www.app.test", "A")
	s.expectHTTPS("panel.test", "panel")

	// Plain HTTP for a known hostname redirects, keeping path and query.
	code, _, loc := s.get(s.httpPort, false, "app.test")
	if code != 308 || loc != "https://app.test/hello?x=1" {
		t.Errorf("http app.test = %d location %q", code, loc)
	}
	if code, _, _ := s.get(s.httpPort, false, "unknown.example.com"); code != 404 {
		t.Errorf("http unknown host = %d, want the panel backend's 404", code)
	}

	// Atomic switch of one route; the other routes keep working.
	if err := m.SetUpstreams(ctx, "app1", []string{s.upstream("B")}); err != nil {
		t.Fatalf("SetUpstreams: %v", err)
	}
	if code, body, _ := s.get(0, true, "app.test"); code != 200 || body != "B" {
		t.Errorf("after switch: %d %q, want B immediately", code, body)
	}
	s.expectHTTPS("panel.test", "panel")
	if err := m.SetUpstreams(ctx, "nope", []string{s.upstream("B")}); err == nil {
		t.Error("SetUpstreams on an unknown route succeeded")
	}

	// Caddy restarts on its own (crash, reboot): it boots from the file and still serves B.
	if err := s.ex.StopContainer(ctx, after.ID, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.ex.StartContainer(ctx, after.ID); err != nil {
		t.Fatal(err)
	}
	s.expectHTTPS("app.test", "B")

	// Stopped app: no upstreams answers 503, not a hang.
	if err := m.SetUpstreams(ctx, "app1", nil); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := s.get(0, true, "app.test"); code != 503 {
		t.Errorf("no upstreams = %d, want 503", code)
	}

	// Supervision: a removed container comes back with the configuration from the source.
	s.set(Config{PanelDomain: "panel.test", Routes: []Route{{ID: "app1", Hosts: []string{"app.test"}, Upstreams: []string{s.upstream("B")}}}})
	if err := s.ex.RemoveContainer(ctx, after.ID, true); err != nil {
		t.Fatal(err)
	}
	superCtx, stopSuper := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { m.Supervise(superCtx, 500*time.Millisecond); close(done) }()
	s.expectHTTPS("app.test", "B")
	stopSuper()
	<-done

	// A changed spec (new HTTP port) replaces the container.
	newPort := freePort(t)
	m2 := s.manager(newPort)
	if err := m2.Start(ctx); err != nil {
		t.Fatalf("Start with new port: %v", err)
	}
	s.eventually("new http port", func() (bool, string) {
		code, _, loc := s.get(newPort, false, "app.test")
		return code == 308, fmt.Sprintf("got %d %q", code, loc)
	})
	if code, body, _ := s.get(s.httpPort, false, "app.test"); code != 0 {
		t.Errorf("old port still answers: %d %q", code, body)
	}

	// An invalid configuration is refused before anything is touched.
	s.set(Config{PanelDomain: "Not A Host"})
	if err := m2.Apply(ctx); err == nil {
		t.Error("Apply accepted an invalid hostname")
	}
	s.expectHTTPS("app.test", "B")
}

func TestManagerRefusesForeignContainer(t *testing.T) {
	s := newStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Something else already owns the proxy's name.
	if _, err := s.ex.CreateContainer(ctx, executor.ContainerSpec{
		Name: s.name + "-proxy", Image: backendImage, Kind: executor.KindApp, ResourceID: "squatter", Command: []string{"sleep", "60"},
	}); err != nil {
		t.Fatal(err)
	}
	err := s.manager(s.httpPort).Start(ctx)
	if err == nil || !strings.Contains(err.Error(), "not managed") {
		t.Fatalf("Start = %v, want a refusal", err)
	}
}

func TestNewValidation(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	src := func(context.Context) (Config, error) { return Config{}, nil }
	ex := &executor.Docker{}
	if _, err := New(Options{Executor: ex, Log: log, Source: src, Admin: "carrier-pigeon", Dir: "/tmp/x"}); err == nil {
		t.Error("unknown admin mode accepted")
	}
	if _, err := New(Options{Executor: ex, Log: log, Source: src, Admin: AdminSocket, Dir: "/" + strings.Repeat("d", 120)}); err == nil {
		t.Error("over-long socket path accepted")
	}
	if _, err := New(Options{Log: log, Source: src, Admin: AdminTCP}); err == nil {
		t.Error("missing executor accepted")
	}
}
