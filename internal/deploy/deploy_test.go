package deploy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"panel/internal/executor"
	"panel/internal/executor/executortest"
	"panel/internal/store"
)

type fakeRouter struct {
	mu        sync.Mutex
	known     map[string]bool     // routes present in the "running configuration"
	upstreams map[string][]string // last upstreams set per route
	applies   int
	failAll   bool
}

func (r *fakeRouter) SetUpstreams(_ context.Context, id string, ups []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failAll || !r.known[id] {
		return errors.New("no such route")
	}
	r.upstreams[id] = ups
	return nil
}

func (r *fakeRouter) Apply(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failAll {
		return errors.New("proxy down")
	}
	r.applies++
	return nil
}

func (r *fakeRouter) last(id string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.upstreams[id]
}

type env struct {
	t      *testing.T
	st     *store.Store
	ex     *executortest.Fake
	router *fakeRouter
	svc    *Service
	dir    string
	app    store.App
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, "Shop", "")
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, p.ID, store.AppInput{Name: "web", Image: "nginx:1", Port: 80, CPULimit: 1, MemoryMB: 64})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, ex: executortest.New(), dir: dir, app: app,
		router: &fakeRouter{known: map[string]bool{}, upstreams: map[string][]string{}}}
	e.svc = New(ctx, Options{
		Store: st, Executor: e.ex, Router: e.router, DataDir: dir,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		StartGrace: 20 * time.Millisecond,
	})
	return e
}

// deploy runs one deployment to its end and returns the final record.
func (e *env) deploy() store.Deployment {
	e.t.Helper()
	dep, err := e.svc.Deploy(context.Background(), e.app.ID, "manual")
	if err != nil {
		e.t.Fatalf("Deploy: %v", err)
	}
	if dep.Status != store.DeployQueued {
		e.t.Fatalf("returned status = %q, want queued", dep.Status)
	}
	e.svc.Wait()
	dep, err = e.st.GetDeployment(context.Background(), dep.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return dep
}

func (e *env) containers() []executor.Container {
	list, _ := e.ex.ListContainers(context.Background())
	return list
}

func (e *env) setImage(image string) {
	e.t.Helper()
	a, err := e.st.UpdateApp(context.Background(), e.app.ID, store.AppInput{Name: "web", Image: image, Port: e.app.Port, CPULimit: 1, MemoryMB: 64})
	if err != nil {
		e.t.Fatal(err)
	}
	e.app = a
}

func TestDeployReplacesContainer(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	if _, err := e.st.CreateDomain(ctx, e.app.ID, "shop.example.com", ""); err != nil {
		t.Fatal(err)
	}

	// First deploy: the route is not in the running configuration yet, so the full rebuild is used.
	first := e.deploy()
	if first.Status != store.DeploySucceeded || first.Error != "" || first.ContainerName == "" || first.FinishedAt.IsZero() {
		t.Fatalf("first deployment = %+v", first)
	}
	cs := e.containers()
	if len(cs) != 1 || !cs[0].Running || cs[0].Name != first.ContainerName || cs[0].ResourceID != e.app.ID || cs[0].Labels["deployment"] != first.ID {
		t.Fatalf("containers = %+v", cs)
	}
	if strings.ToLower(first.ContainerName) != first.ContainerName || len(first.ContainerName) > 63 {
		t.Errorf("container name %q is not a DNS label", first.ContainerName)
	}
	app, _ := e.st.GetApp(ctx, e.app.ID)
	if app.CurrentDeploymentID != first.ID || app.DesiredState != store.StateRunning {
		t.Fatalf("app after deploy = %+v", app)
	}
	if e.router.applies != 1 {
		t.Errorf("applies = %d, want the fallback rebuild once", e.router.applies)
	}

	// Second deploy: atomic switch, old container removed.
	e.router.known[e.app.ID] = true
	e.setImage("nginx:2")
	second := e.deploy()
	if second.Status != store.DeploySucceeded || second.Image != "nginx:2" {
		t.Fatalf("second deployment = %+v", second)
	}
	cs = e.containers()
	if len(cs) != 1 || cs[0].Name != second.ContainerName || cs[0].Image != "nginx:2" {
		t.Fatalf("containers after redeploy = %+v", cs)
	}
	if got := e.router.last(e.app.ID); len(got) != 1 || got[0] != second.ContainerName+":80" {
		t.Errorf("route upstreams = %v", got)
	}
	if e.router.applies != 1 {
		t.Errorf("applies = %d, the second switch should be the atomic patch", e.router.applies)
	}

	// The log tells the story.
	raw, err := os.ReadFile(e.svc.LogPath(second))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Pulling image nginx:2", "abc123: Pull complete", "Switching traffic", "Removing old container " + first.ContainerName, "Deployment succeeded"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("log lacks %q:\n%s", want, raw)
		}
	}

	// Events were recorded.
	events, _ := e.st.ListEvents(ctx, 10)
	if len(events) != 2 || events[0].Action != "deployment.succeeded" || events[0].Actor != "system" {
		t.Errorf("events = %+v", events)
	}
}

func TestDeployFailureKeepsOldContainer(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	if _, err := e.st.CreateDomain(ctx, e.app.ID, "shop.example.com", ""); err != nil {
		t.Fatal(err)
	}
	e.router.known[e.app.ID] = true
	good := e.deploy()
	wantRoute := e.router.last(e.app.ID)

	check := func(what string, dep store.Deployment, wantErr string) {
		t.Helper()
		if dep.Status != store.DeployFailed || !strings.Contains(dep.Error, wantErr) || dep.FinishedAt.IsZero() {
			t.Errorf("%s: deployment = %+v, want failed with %q", what, dep, wantErr)
		}
		cs := e.containers()
		if len(cs) != 1 || cs[0].Name != good.ContainerName || !cs[0].Running {
			t.Errorf("%s: containers = %+v, want only the old one, running", what, cs)
		}
		app, _ := e.st.GetApp(ctx, e.app.ID)
		if app.CurrentDeploymentID != good.ID || app.DesiredState != store.StateRunning {
			t.Errorf("%s: app = %+v, want still on the old deployment", what, app)
		}
		if got := e.router.last(e.app.ID); len(got) != 1 || got[0] != wantRoute[0] {
			t.Errorf("%s: route = %v, want untouched %v", what, got, wantRoute)
		}
	}

	e.setImage("registry.example/missing:1")
	check("pull fails", e.deploy(), "manifest unknown")

	e.setImage("crasher:1")
	e.ex.CrashOnStart["crasher:1"] = true
	dep := e.deploy()
	check("crash on start", dep, "exited right after start (exit code 1): fatal: cannot start")
	raw, _ := os.ReadFile(e.svc.LogPath(dep))
	if !strings.Contains(string(raw), "| fatal: cannot start") {
		t.Errorf("the container's output is not in the log:\n%s", raw)
	}

	// Exits with code 0 and is restarted by Docker: "running", but not serving.
	e.setImage("shell:1")
	e.ex.RestartLoop["shell:1"] = true
	check("restart loop", e.deploy(), "exited right after start (exit code 0)")

	e.setImage("nginx:3")
	e.router.failAll = true
	check("proxy down", e.deploy(), "switch route")
	e.router.failAll = false

	// And it still deploys fine afterwards.
	if dep := e.deploy(); dep.Status != store.DeploySucceeded {
		t.Fatalf("deploy after failures = %+v", dep)
	}
}

func TestDeployWithoutDomainsSkipsProxy(t *testing.T) {
	e := newEnv(t)
	e.router.failAll = true // would fail if it were called
	if dep := e.deploy(); dep.Status != store.DeploySucceeded {
		t.Fatalf("deployment = %+v", dep)
	}
}

func TestOneOperationPerApp(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	e.ex.PullDelay = 150 * time.Millisecond
	if _, err := e.svc.Deploy(ctx, e.app.ID, "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Deploy(ctx, e.app.ID, "manual"); !errors.Is(err, ErrBusy) {
		t.Errorf("second deploy = %v, want ErrBusy", err)
	}
	if err := e.svc.Stop(ctx, e.app.ID); !errors.Is(err, ErrBusy) {
		t.Errorf("stop during deploy = %v, want ErrBusy", err)
	}
	if err := e.svc.Remove(ctx, e.app.ID); !errors.Is(err, ErrBusy) {
		t.Errorf("remove during deploy = %v, want ErrBusy", err)
	}
	e.svc.Wait()
	if list, _ := e.st.ListDeployments(ctx, e.app.ID, 10); len(list) != 1 {
		t.Errorf("deployments = %d, the refused one must not leave a row", len(list))
	}
	if _, err := e.svc.Deploy(ctx, "missing", "manual"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deploy unknown app = %v", err)
	}
	e.svc.Wait()
}

func TestStopStartRestart(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	if _, err := e.st.CreateDomain(ctx, e.app.ID, "shop.example.com", ""); err != nil {
		t.Fatal(err)
	}
	e.router.known[e.app.ID] = true

	for name, fn := range map[string]func(context.Context, string) error{"stop": e.svc.Stop, "start": e.svc.Start, "restart": e.svc.Restart} {
		if err := fn(ctx, e.app.ID); !errors.Is(err, ErrNotDeployed) {
			t.Errorf("%s before first deploy = %v, want ErrNotDeployed", name, err)
		}
	}
	if got := e.svc.RuntimeState(ctx, e.app.ID); got != "none" {
		t.Errorf("runtime state before deploy = %q", got)
	}

	dep := e.deploy()
	upstream := dep.ContainerName + ":80"
	if got := e.svc.RuntimeState(ctx, e.app.ID); got != "running" {
		t.Errorf("runtime state = %q", got)
	}

	if err := e.svc.Stop(ctx, e.app.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	app, _ := e.st.GetApp(ctx, e.app.ID)
	if cs := e.containers(); len(cs) != 1 || cs[0].Running || app.DesiredState != store.StateStopped {
		t.Fatalf("after stop: containers=%+v app=%+v", cs, app)
	}
	if got := e.router.last(e.app.ID); len(got) != 0 {
		t.Errorf("route after stop = %v, want none (503)", got)
	}
	if got := e.svc.RuntimeState(ctx, e.app.ID); got != "exited" {
		t.Errorf("runtime state after stop = %q", got)
	}

	if err := e.svc.Start(ctx, e.app.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	app, _ = e.st.GetApp(ctx, e.app.ID)
	if cs := e.containers(); !cs[0].Running || app.DesiredState != store.StateRunning {
		t.Fatalf("after start: containers=%+v app=%+v", cs, app)
	}
	if got := e.router.last(e.app.ID); len(got) != 1 || got[0] != upstream {
		t.Errorf("route after start = %v", got)
	}

	if err := e.svc.Restart(ctx, e.app.ID); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if cs := e.containers(); len(cs) != 1 || !cs[0].Running || cs[0].Name != dep.ContainerName {
		t.Fatalf("after restart: %+v", cs)
	}

	// The container vanished behind the panel's back.
	_ = e.ex.RemoveContainer(ctx, dep.ContainerName, true)
	if got := e.svc.RuntimeState(ctx, e.app.ID); got != "missing" {
		t.Errorf("runtime state = %q, want missing", got)
	}
	if err := e.svc.Start(ctx, e.app.ID); !errors.Is(err, ErrNotDeployed) {
		t.Errorf("start with a missing container = %v", err)
	}
	if err := e.svc.Stop(ctx, e.app.ID); err != nil {
		t.Errorf("stop with a missing container = %v, want success", err)
	}
}

func TestRemove(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	dep := e.deploy()
	// A container of another app must survive.
	other, _ := e.ex.CreateContainer(ctx, executor.ContainerSpec{Name: "other", Image: "x", Kind: executor.KindApp, ResourceID: "another-app"})
	if err := e.svc.Remove(ctx, e.app.ID); err != nil {
		t.Fatal(err)
	}
	if cs := e.containers(); len(cs) != 1 || cs[0].ID != other {
		t.Errorf("containers after remove = %+v", cs)
	}
	if _, err := os.Stat(e.svc.LogPath(dep)); !os.IsNotExist(err) {
		t.Errorf("deployment log survived: %v", err)
	}
}

func TestRecoverAndShutdown(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	stale, _ := e.st.CreateDeployment(ctx, e.app.ID, "manual", "nginx:1", 80, func(string) string { return "" })
	n, err := e.svc.Recover(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Recover = %d, %v", n, err)
	}
	if got, _ := e.st.GetDeployment(ctx, stale.ID); got.Status != store.DeployFailed || !strings.Contains(got.Error, "restarted") {
		t.Errorf("stale deployment = %+v", got)
	}

	// A deploy cut short by shutdown records that, and leaves no container.
	runCtx, cancel := context.WithCancel(ctx)
	svc := New(runCtx, Options{Store: e.st, Executor: e.ex, DataDir: e.dir, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	e.ex.PullDelay = 5 * time.Second
	dep, err := svc.Deploy(ctx, e.app.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	svc.Wait()
	got, _ := e.st.GetDeployment(ctx, dep.ID)
	if got.Status != store.DeployFailed || !strings.Contains(got.Error, "shutting down") {
		t.Errorf("cancelled deployment = %+v", got)
	}
	if cs := e.containers(); len(cs) != 0 {
		t.Errorf("containers after cancelled deploy = %+v", cs)
	}
}
