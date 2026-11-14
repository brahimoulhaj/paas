package deploy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"panel/internal/builder"
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
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		StartGrace:  20 * time.Millisecond,
		RetryDelays: []time.Duration{time.Millisecond, time.Millisecond},
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

// imageApp adds another image app to the project.
func (e *env) imageApp(name string) store.App {
	e.t.Helper()
	a, err := e.st.CreateApp(context.Background(), e.app.ProjectID, store.AppInput{Name: name, Image: "nginx:1", Port: 80})
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

// leftOpen records a deployment as a previous panel process left it: queued, or running
// (started `starts` times) with its container created when withContainer is set.
func (e *env) leftOpen(app store.App, starts int, withContainer bool) store.Deployment {
	e.t.Helper()
	ctx := context.Background()
	dep, err := e.st.CreateDeployment(ctx, app.ID, "manual", app.Image, app.Port, func(id string) string {
		return filepath.Join("deployments", app.ID, id+".log")
	})
	if err != nil {
		e.t.Fatal(err)
	}
	for range starts {
		if err := e.st.MarkDeploymentRunning(ctx, dep.ID); err != nil {
			e.t.Fatal(err)
		}
	}
	if withContainer {
		name := containerName(app.ID, dep.ID)
		id, err := e.ex.CreateContainer(ctx, executor.ContainerSpec{Name: name, Image: app.Image, Kind: executor.KindApp, ResourceID: app.ID})
		if err != nil {
			e.t.Fatal(err)
		}
		if err := e.st.SetDeploymentContainer(ctx, dep.ID, id, name); err != nil {
			e.t.Fatal(err)
		}
	}
	got, _ := e.st.GetDeployment(ctx, dep.ID)
	return got
}

func (e *env) log(dep store.Deployment) string {
	raw, _ := os.ReadFile(e.svc.LogPath(dep))
	return string(raw)
}

func TestRecoverResumesDeployments(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	queued := e.leftOpen(e.app, 0, false)                 // never started
	interrupted := e.leftOpen(e.imageApp("api"), 1, true) // cut off before the switch, container made
	exhausted := e.leftOpen(e.imageApp("cron"), 3, false) // the panel restarted under it 3 times

	// Cut off after the switch: the app already serves it, an older container still runs.
	switchedApp := e.imageApp("docs")
	old := e.leftOpen(switchedApp, 1, true)
	if err := e.st.FinishDeployment(ctx, old.ID, store.DeploySucceeded, ""); err != nil {
		t.Fatal(err)
	}
	switched := e.leftOpen(switchedApp, 1, true)
	if err := e.st.SetAppCurrentDeployment(ctx, switchedApp.ID, switched.ID, store.StateRunning); err != nil {
		t.Fatal(err)
	}

	ready := make(chan struct{})
	e.svc.opts.Ready = ready
	r, err := e.svc.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r != (Recovery{Resumed: 2, Finished: 1, Failed: 1}) {
		t.Fatalf("Recover = %+v", r)
	}
	// The interrupted attempt's container is gone at once; the resumed ones wait for Ready.
	for _, c := range e.containers() {
		if c.Name == interrupted.ContainerName || c.Name == old.ContainerName {
			t.Errorf("container %s left after Recover", c.Name)
		}
	}
	time.Sleep(30 * time.Millisecond)
	for _, d := range []store.Deployment{queued, interrupted} {
		if got, _ := e.st.GetDeployment(ctx, d.ID); got.Status != store.DeployQueued {
			t.Fatalf("%s started before Ready: %s", d.ID, got.Status)
		}
	}
	close(ready)
	e.svc.Wait()

	if d := e.waitStatus(queued.ID, store.DeploySucceeded); d.Attempts != 1 {
		t.Errorf("queued: %+v", d)
	}
	d := e.waitStatus(interrupted.ID, store.DeploySucceeded)
	if d.Attempts != 2 || d.ContainerName == "" || !strings.Contains(e.log(d), "starting again (attempt 2 of 3)") {
		t.Errorf("interrupted: %+v\n%s", d, e.log(d))
	}
	if d := e.waitStatus(exhausted.ID, store.DeployFailed); !strings.Contains(d.Error, "restarted during this deployment 3 times") {
		t.Errorf("exhausted: %+v", d)
	}
	if d := e.waitStatus(switched.ID, store.DeploySucceeded); !strings.Contains(e.log(d), "after switching traffic") {
		t.Errorf("switched: %+v\n%s", d, e.log(d))
	}
	var names []string
	for _, c := range e.containers() {
		if c.ResourceID == switchedApp.ID {
			names = append(names, c.Name)
		}
	}
	if len(names) != 1 || names[0] != switched.ContainerName {
		t.Errorf("containers of the switched app = %v, want only %s", names, switched.ContainerName)
	}
}

func TestShutdownRequeues(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	b := &fakeBuilder{commit: strings.Repeat("a", 40), gate: make(chan struct{}), started: make(chan string, 4)}
	e.useGit(b)
	other := e.gitApp("api")

	// One deploy builds, one waits for the build slot; then the panel shuts down.
	runCtx, cancel := context.WithCancel(ctx)
	svc := New(runCtx, Options{Store: e.st, Executor: e.ex, Router: e.router, Builder: b, DataDir: e.dir,
		Log: e.svc.opts.Log, StartGrace: 20 * time.Millisecond})
	building, err := svc.Deploy(ctx, e.app.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-b.started
	waiting, err := svc.Deploy(ctx, other.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(e.log(waiting), "Waiting for a running build") {
		if time.Now().After(deadline) {
			t.Fatal("second deploy never waited for the slot")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	svc.Wait()
	for _, d := range []store.Deployment{building, waiting} {
		got, _ := e.st.GetDeployment(ctx, d.ID)
		if got.Status != store.DeployQueued || got.Error != "" || !strings.Contains(e.log(got), "starts again when it is back") {
			t.Errorf("after shutdown: %+v\n%s", got, e.log(got))
		}
	}
	if cs := e.containers(); len(cs) != 0 {
		t.Errorf("containers after shutdown = %+v", cs)
	}

	// The next start picks both up.
	close(b.gate)
	e.svc.opts.Builder = b
	if r, err := e.svc.Recover(ctx); err != nil || r.Resumed != 2 {
		t.Fatalf("Recover = %+v, %v", r, err)
	}
	e.svc.Wait()
	e.waitStatus(building.ID, store.DeploySucceeded)
	e.waitStatus(waiting.ID, store.DeploySucceeded)
}

func TestRetryTemporaryFailures(t *testing.T) {
	e := newEnv(t)
	b := &fakeBuilder{commit: strings.Repeat("a", 40)}
	e.useGit(b)

	// Two network failures, then the clone works: retried, deployed.
	b.fetchErr = errors.New("git clone: exit status 128: fatal: unable to access 'https://example.com/': Could not resolve host: example.com")
	b.fetchFails = 2
	dep := e.deploy()
	if dep.Status != store.DeploySucceeded || len(b.dirs) != 3 {
		t.Fatalf("after 2 temporary failures: %+v, %d fetches\n%s", dep, len(b.dirs), e.log(dep))
	}
	if l := e.log(dep); strings.Count(l, "Trying again in") != 2 || !strings.Contains(l, "retry 2 of 2") {
		t.Errorf("log:\n%s", l)
	}

	// Three in a row: out of retries.
	b.dirs, b.failed, b.fetchFails = nil, 0, 3
	if dep := e.deploy(); dep.Status != store.DeployFailed || len(b.dirs) != 3 || !strings.Contains(dep.Error, "Could not resolve host") {
		t.Errorf("3 temporary failures: %+v, %d fetches", dep, len(b.dirs))
	}

	// A lasting failure is not retried.
	b.dirs, b.failed, b.fetchFails = nil, 0, 1
	b.fetchErr = errors.New("git clone: exit status 128: fatal: Remote branch main not found in upstream origin")
	if dep := e.deploy(); dep.Status != store.DeployFailed || len(b.dirs) != 1 {
		t.Errorf("lasting failure: %+v, %d fetches", dep, len(b.dirs))
	}
}

func TestTemporary(t *testing.T) {
	for msg, want := range map[string]bool{
		"Could not resolve host: github.com":                                        true,
		"dial tcp: lookup registry-1.docker.io: no such host":                       true,
		"Get \"https://registry-1.docker.io/v2/\": net/http: TLS handshake timeout": true,
		"read tcp 10.0.0.2:443: connection reset by peer":                           true,
		"error: RPC failed; curl 56 GnuTLS recv error":                              true,
		"The requested URL returned error: 503":                                     true,
		"pull nginx:nope: manifest unknown":                                         false,
		"Authentication failed for 'https://gitlab.com/org/app.git/'":               false,
		"Remote branch dev not found in upstream origin":                            false,
	} {
		if got := temporary(errors.New(msg)); got != want {
			t.Errorf("temporary(%q) = %v, want %v", msg, got, want)
		}
	}
	if temporary(context.Canceled) {
		t.Error("a cancel is not temporary")
	}
}

// fakeBuilder "clones" by creating the workspace and "builds" by recording the tag.
type fakeBuilder struct {
	mu         sync.Mutex
	commit     string
	message    string // the commit's message
	fetchErr   error
	fetchFails int // when set, only this many fetches fail with fetchErr
	failed     int // fetches failed so far
	buildErr   error
	auth       builder.Auth       // credentials of the last fetch
	dirs       []string           // workspaces it was given
	tags       []string           // images it built
	images     *executortest.Fake // when set, built images are recorded there
	gate       chan struct{}      // when set, a build waits for a value (or its context)
	started    chan string        // when set, receives each tag as its build starts
}

func (b *fakeBuilder) Fetch(_ context.Context, src store.GitSource, auth builder.Auth, dir string, line builder.LineFunc) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.auth = auth
	b.dirs = append(b.dirs, dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	line("Cloning into '" + dir + "'...")
	if b.fetchErr != nil && (b.fetchFails == 0 || b.failed < b.fetchFails) {
		b.failed++
		return "", b.fetchErr
	}
	return b.commit, nil
}

func (b *fakeBuilder) CommitMessage(context.Context, string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.message, nil
}

func (b *fakeBuilder) Build(ctx context.Context, src store.GitSource, dir, tag, appID string, line builder.LineFunc) error {
	if b.started != nil {
		b.started <- tag
	}
	if b.gate != nil {
		select {
		case <-b.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	line("#1 [internal] load build definition from " + src.Dockerfile)
	if b.buildErr != nil {
		return b.buildErr
	}
	b.tags = append(b.tags, tag)
	if b.images != nil {
		b.images.AddImage(appID, tag)
	}
	return nil
}

func (e *env) useGit(b *fakeBuilder) {
	e.t.Helper()
	a, err := e.st.UpdateApp(context.Background(), e.app.ID, store.AppInput{
		Name: "web", SourceType: store.SourceGit, Port: 8080,
		Git: store.GitSource{Repository: "https://example.com/org/web.git", Branch: "main", Builder: store.BuilderDockerfile, Dockerfile: "Dockerfile"},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.app = a
	e.svc.opts.Builder = b
}

func TestDeployFromGit(t *testing.T) {
	e := newEnv(t)
	sha := strings.Repeat("ab12", 10)
	b := &fakeBuilder{commit: sha}
	e.useGit(b)
	if e.app.Image != "" {
		t.Fatalf("a Git app keeps no image: %+v", e.app)
	}

	dep := e.deploy()
	wantTag := "app-" + strings.ToLower(e.app.ID) + ":" + sha
	if dep.Status != store.DeploySucceeded || dep.CommitSHA != sha || dep.Image != wantTag || dep.Port != 8080 {
		t.Fatalf("deployment = %+v, want succeeded with %s", dep, wantTag)
	}
	if len(b.tags) != 1 || b.tags[0] != wantTag {
		t.Errorf("built %v", b.tags)
	}
	if cs := e.containers(); len(cs) != 1 || cs[0].Image != wantTag {
		t.Errorf("containers = %+v", cs)
	}
	if _, err := os.Stat(b.dirs[0]); !os.IsNotExist(err) {
		t.Errorf("workspace %s still there (err %v)", b.dirs[0], err)
	}
	raw, _ := os.ReadFile(e.svc.LogPath(dep))
	for _, want := range []string{"from https://example.com/org/web.git (branch main)", "Commit " + sha, "| #1 [internal] load build definition", "Built image " + wantTag, "Deployment succeeded"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("log lacks %q:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), "Pulling image") {
		t.Errorf("a Git deploy pulled:\n%s", raw)
	}
}

func TestDeployFromGitFailures(t *testing.T) {
	e := newEnv(t)
	good := e.deploy() // the image deploy that keeps serving
	b := &fakeBuilder{commit: strings.Repeat("c", 40)}
	e.useGit(b)

	b.fetchErr = errors.New("git clone: exit status 128: fatal: repository not found")
	dep := e.deploy()
	if dep.Status != store.DeployFailed || !strings.Contains(dep.Error, "repository not found") || dep.CommitSHA != "" {
		t.Errorf("fetch failure: %+v", dep)
	}

	b.fetchErr, b.buildErr = nil, errors.New("docker build: exit status 1: ERROR: failed to solve")
	dep = e.deploy()
	if dep.Status != store.DeployFailed || !strings.Contains(dep.Error, "failed to solve") || dep.CommitSHA == "" || dep.Image != "" {
		t.Errorf("build failure: %+v", dep)
	}
	if cs := e.containers(); len(cs) != 1 || cs[0].Name != good.ContainerName {
		t.Errorf("containers = %+v, want only the old one", cs)
	}
	for _, d := range b.dirs {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("workspace %s left behind", d)
		}
	}

	e.svc.opts.Builder = nil
	if dep := e.deploy(); dep.Status != store.DeployFailed || !strings.Contains(dep.Error, "cannot build from Git") {
		t.Errorf("no builder: %+v", dep)
	}
}

type fakeGitAuth struct {
	err   error
	calls []string
}

func (a *fakeGitAuth) Credentials(_ context.Context, connectionID, repository string) (builder.Auth, error) {
	a.calls = append(a.calls, connectionID+" "+repository)
	return builder.Auth{Username: "x-access-token", Password: "s3cret-token"}, a.err
}

func TestDeployFromPrivateGit(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	b := &fakeBuilder{commit: strings.Repeat("d", 40)}
	e.useGit(b)
	if dep := e.deploy(); dep.Status != store.DeploySucceeded || b.auth != (builder.Auth{}) {
		t.Fatalf("public repository: %+v, auth %+v", dep, b.auth)
	}

	// The app now uses a Git connection.
	conn, err := e.st.CreateGitConnection(ctx, store.GitTypeGitLab, "gl", "https://example.com", "sealed")
	if err != nil {
		t.Fatal(err)
	}
	in := store.AppInput{Name: "web", SourceType: store.SourceGit, Port: 8080, Git: e.app.Git}
	in.Git.ConnectionID = conn.ID
	if e.app, err = e.st.UpdateApp(ctx, e.app.ID, in); err != nil {
		t.Fatal(err)
	}

	if dep := e.deploy(); dep.Status != store.DeployFailed || !strings.Contains(dep.Error, "cannot fetch private repositories") {
		t.Errorf("no GitAuth: %+v", dep)
	}

	ga := &fakeGitAuth{}
	e.svc.opts.GitAuth = ga
	dep := e.deploy()
	if dep.Status != store.DeploySucceeded || b.auth.Password != "s3cret-token" {
		t.Fatalf("private repository: %+v, auth %+v", dep, b.auth)
	}
	if len(ga.calls) != 1 || ga.calls[0] != conn.ID+" https://example.com/org/web.git" {
		t.Errorf("credentials asked for %v", ga.calls)
	}
	if raw, _ := os.ReadFile(e.svc.LogPath(dep)); strings.Contains(string(raw), "s3cret-token") {
		t.Errorf("the token is in the deployment log:\n%s", raw)
	}

	ga.err = errors.New("the GitHub App is not installed on org/web")
	if dep := e.deploy(); dep.Status != store.DeployFailed || !strings.Contains(dep.Error, "git connection: the GitHub App is not installed") {
		t.Errorf("credentials failure: %+v", dep)
	}
}

type environmentExecutor struct {
	executor.Executor
	environment map[string]string
}

func (e *environmentExecutor) CreateContainer(ctx context.Context, spec executor.ContainerSpec) (string, error) {
	e.environment = spec.Env
	return e.Executor.CreateContainer(ctx, spec)
}
func TestDeployEnvironment(t *testing.T) {
	e := newEnv(t)
	values := map[string]string{"APP_ENV": "production", "TOKEN": "a=b#c", "EMPTY": ""}
	var err error
	e.app, err = e.st.UpdateApp(context.Background(), e.app.ID, store.AppInput{
		Name: e.app.Name, Image: e.app.Image, Port: e.app.Port, Environment: values,
	})
	if err != nil {
		t.Fatal(err)
	}
	ex := &environmentExecutor{Executor: e.ex}
	e.svc = New(context.Background(), Options{Store: e.st, Executor: ex, Router: e.router, DataDir: e.dir, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), StartGrace: 20 * time.Millisecond})
	d := e.deploy()
	if d.Status != store.DeploySucceeded {
		t.Fatalf("deployment failed: %s", d.Error)
	}
	for k, v := range values {
		if got, ok := ex.environment[k]; !ok || got != v {
			t.Fatalf("variable %s not passed to container", k)
		}
	}
}

func TestDeployPrunesBuildImages(t *testing.T) {
	e := newEnv(t)
	b := &fakeBuilder{images: e.ex}
	e.useGit(b)
	ctx := context.Background()
	tags := func() []string {
		imgs, err := e.ex.ListBuildImages(ctx, e.app.ID)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, img := range imgs {
			if len(img.Tags) == 0 {
				out = append(out, "<dangling>")
			}
			out = append(out, img.Tags...)
		}
		return out
	}

	var built []string
	for i := range KeepImages + 2 {
		b.commit = strings.Repeat(string(rune('a'+i)), 40)
		dep := e.deploy()
		if dep.Status != store.DeploySucceeded {
			t.Fatalf("deploy %d: %+v", i, dep)
		}
		built = append(built, dep.Image)
	}
	want := slices.Clone(built[len(built)-KeepImages:])
	slices.Reverse(want)
	if got := tags(); !slices.Equal(got, want) {
		t.Fatalf("images after %d deploys = %v, want newest %d: %v", len(built), got, KeepImages, want)
	}

	// Rebuilding the live commit moves its tag to a new image; the old one is dangling and goes.
	dep := e.deploy()
	if dep.Status != store.DeploySucceeded {
		t.Fatalf("rebuild: %+v", dep)
	}
	if got := tags(); !slices.Equal(got, want) {
		t.Errorf("images after rebuild = %v, want %v (no dangling)", got, want)
	}
	log, _ := os.ReadFile(e.svc.LogPath(dep))
	if !strings.Contains(string(log), "Removed old image sha256:") {
		t.Errorf("rebuild log does not mention the dangling image:\n%s", log)
	}

	if err := e.svc.Remove(ctx, e.app.ID); err != nil {
		t.Fatal(err)
	}
	if got := tags(); len(got) != 0 {
		t.Errorf("images after Remove = %v", got)
	}
}

func TestContainerEnvPort(t *testing.T) {
	img := store.App{SourceType: store.SourceImage, Port: 80, Environment: map[string]string{"A": "1"}}
	if env := containerEnv(img, 80); len(env) != 1 {
		t.Errorf("image app env = %v, want untouched", env)
	}
	git := store.App{SourceType: store.SourceGit, Port: 8080, Environment: map[string]string{"A": "1"}}
	if env := containerEnv(git, 8080); env["PORT"] != "8080" || env["A"] != "1" {
		t.Errorf("git app env = %v, want PORT=8080 added", env)
	}
	if _, ok := git.Environment["PORT"]; ok {
		t.Error("containerEnv changed the app's own map")
	}
	git.Environment["PORT"] = "3000"
	if env := containerEnv(git, 8080); env["PORT"] != "3000" {
		t.Errorf("user PORT overridden: %v", env)
	}
}

func TestStartCommand(t *testing.T) {
	if ep, cmd := startCommand(store.App{}); ep != nil || cmd != nil {
		t.Errorf("no start command = %v %v, want the image's own", ep, cmd)
	}
	ep, cmd := startCommand(store.App{StartCommand: "java -jar $(ls build/libs/*.jar)"})
	if strings.Join(ep, " ") != "/bin/sh -c" || len(cmd) != 1 || cmd[0] != "java -jar $(ls build/libs/*.jar)" {
		t.Errorf("start command = %q %q", ep, cmd)
	}
}

// gitApp adds another Git app to the env's project.
func (e *env) gitApp(name string) store.App {
	e.t.Helper()
	a, err := e.st.CreateApp(context.Background(), e.app.ProjectID, store.AppInput{
		Name: name, SourceType: store.SourceGit, Port: 8080,
		Git: store.GitSource{Repository: "https://example.com/org/" + name + ".git", Branch: "main", Builder: store.BuilderDockerfile, Dockerfile: "Dockerfile"},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

// waitStatus polls a deployment until it has the status, or fails the test.
func (e *env) waitStatus(id, status string) store.Deployment {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		d, err := e.st.GetDeployment(context.Background(), id)
		if err != nil {
			e.t.Fatal(err)
		}
		if d.Status == status {
			return d
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("deployment %s is %s (%s), want %s", id, d.Status, d.Error, status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBuildsRunOneAtATime(t *testing.T) {
	e := newEnv(t)
	b := &fakeBuilder{commit: strings.Repeat("a", 40), gate: make(chan struct{}), started: make(chan string, 4)}
	e.useGit(b)
	other := e.gitApp("api")
	ctx := context.Background()

	first, err := e.svc.Deploy(ctx, e.app.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-b.started // the first build holds the only slot
	second, err := e.svc.Deploy(ctx, other.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	// The second waits, still queued, and says why.
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(e.svc.LogPath(second))
		if strings.Contains(string(raw), "Waiting for a running build to finish (1 at a time)") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("second deploy log:\n%s", raw)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if d, _ := e.st.GetDeployment(ctx, second.ID); d.Status != store.DeployQueued {
		t.Fatalf("second deploy is %s while the first builds, want queued", d.Status)
	}
	select {
	case tag := <-b.started:
		t.Fatalf("second build %s started while the first was running", tag)
	case <-time.After(50 * time.Millisecond):
	}

	b.gate <- struct{}{} // first build finishes; its slot goes to the second
	<-b.started
	b.gate <- struct{}{}
	e.svc.Wait()
	for _, id := range []string{first.ID, second.ID} {
		if d := e.waitStatus(id, store.DeploySucceeded); d.Image == "" {
			t.Errorf("deployment %s: %+v", id, d)
		}
	}
}

func TestMaxBuildsRunsInParallel(t *testing.T) {
	e := newEnv(t)
	e.svc = New(context.Background(), Options{Store: e.st, Executor: e.ex, Router: e.router, DataDir: e.dir,
		Log: e.svc.opts.Log, StartGrace: 20 * time.Millisecond, MaxBuilds: 2})
	b := &fakeBuilder{commit: strings.Repeat("a", 40), gate: make(chan struct{}), started: make(chan string, 4)}
	e.useGit(b)
	other := e.gitApp("api")
	for _, id := range []string{e.app.ID, other.ID} {
		if _, err := e.svc.Deploy(context.Background(), id, "manual"); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		select {
		case <-b.started:
		case <-time.After(5 * time.Second):
			t.Fatal("two builds did not run at once with MaxBuilds 2")
		}
	}
	close(b.gate)
	e.svc.Wait()
}

func TestCancelDeployment(t *testing.T) {
	e := newEnv(t)
	good := e.deploy() // the image deploy that keeps serving
	b := &fakeBuilder{commit: strings.Repeat("c", 40), gate: make(chan struct{}), started: make(chan string, 4)}
	e.useGit(b)
	other := e.gitApp("api")
	ctx := context.Background()

	running, err := e.svc.Deploy(ctx, e.app.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	<-b.started
	queued, err := e.svc.Deploy(ctx, other.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}

	// A queued deploy never starts.
	if err := e.svc.Cancel(queued.ID); err != nil {
		t.Fatal(err)
	}
	if d := e.waitStatus(queued.ID, store.DeployCancelled); d.Error != "cancelled by the user" || !d.StartedAt.IsZero() {
		t.Errorf("cancelled queued deploy: %+v", d)
	}
	// A running build is interrupted; the old container keeps serving.
	if err := e.svc.Cancel(running.ID); err != nil {
		t.Fatal(err)
	}
	e.waitStatus(running.ID, store.DeployCancelled)
	e.svc.Wait()
	if cs := e.containers(); len(cs) != 1 || cs[0].Name != good.ContainerName {
		t.Errorf("containers = %+v, want only the old one", cs)
	}
	if a, _ := e.st.GetApp(ctx, e.app.ID); a.CurrentDeploymentID != good.ID {
		t.Errorf("current deployment = %s, want %s", a.CurrentDeploymentID, good.ID)
	}
	raw, _ := os.ReadFile(e.svc.LogPath(running))
	if !strings.Contains(string(raw), "Deployment cancelled") || strings.Contains(string(raw), "Deployment failed") {
		t.Errorf("log:\n%s", raw)
	}
	events, _ := e.st.ListEvents(ctx, 10)
	if len(events) == 0 || events[0].Action != "deployment.cancelled" {
		t.Errorf("latest event = %+v", events)
	}

	// Finished or unknown deployments cannot be cancelled; the build slot was given back.
	for _, id := range []string{running.ID, good.ID, "nope"} {
		if err := e.svc.Cancel(id); !errors.Is(err, ErrNotCancellable) {
			t.Errorf("Cancel(%s) = %v", id, err)
		}
	}
	close(b.gate)
	if d := e.deploy(); d.Status != store.DeploySucceeded {
		t.Errorf("deploy after cancels: %+v", d)
	}
}

func TestDeployFromPrivateRegistry(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	reg, err := e.st.CreateRegistry(ctx, "GHCR", store.RegistryCredentials{Server: "ghcr.io", Username: "bot", Password: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	e.ex.Private = map[string]bool{"ghcr.io/org/web:1": true}
	e.ex.Logins = map[string]string{"ghcr.io": "bot:tok"}
	update := func(image, registryID string) {
		t.Helper()
		a, err := e.st.UpdateApp(ctx, e.app.ID, store.AppInput{Name: "web", Image: image, RegistryID: registryID, Port: 80})
		if err != nil {
			t.Fatal(err)
		}
		e.app = a
	}

	// Without credentials the private image does not pull.
	update("ghcr.io/org/web:1", "")
	if d := e.deploy(); d.Status != store.DeployFailed || !strings.Contains(d.Error, "unauthorized") {
		t.Fatalf("no registry: %+v", d)
	}
	// With the registry it does, and the log names the account, never the password.
	update("ghcr.io/org/web:1", reg.ID)
	d := e.deploy()
	if d.Status != store.DeploySucceeded {
		t.Fatalf("with registry: %+v\n%s", d, e.log(d))
	}
	if last := e.ex.PullAuths[len(e.ex.PullAuths)-1]; last == nil || last.Username != "bot" || last.Server != "ghcr.io" {
		t.Errorf("pull auth = %+v", last)
	}
	if l := e.log(d); !strings.Contains(l, "signed in to ghcr.io as bot") || strings.Contains(l, "tok") {
		t.Errorf("log:\n%s", l)
	}
	// An image on another host never receives the credentials (set behind the API's back).
	if _, err := e.st.UpdateApp(ctx, e.app.ID, store.AppInput{Name: "web", Image: "evil.example.com/web:1", RegistryID: reg.ID, Port: 80}); err != nil {
		t.Fatal(err)
	}
	pulls := len(e.ex.PullAuths)
	if d := e.deploy(); d.Status != store.DeployFailed || !strings.Contains(d.Error, "only sent to their own registry") || len(e.ex.PullAuths) != pulls {
		t.Errorf("other host: %+v, %d pulls", d, len(e.ex.PullAuths)-pulls)
	}
}

// TestHealthCheck runs saved TCP and HTTP checks against a local server standing in for
// the container: the dialer sends every health check connection there.
func TestHealthCheck(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	if _, err := e.st.CreateDomain(ctx, e.app.ID, "shop.example.com", ""); err != nil {
		t.Fatal(err)
	}
	e.router.known[e.app.ID] = true

	var mu sync.Mutex
	status, hits := http.StatusOK, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		hits++
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	var dialed []string
	e.svc.opts.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, addr)
		mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	save := func(h store.HealthCheck) {
		t.Helper()
		if err := e.st.SetAppHealthCheck(ctx, e.app.ID, h); err != nil {
			t.Fatal(err)
		}
	}
	logOf := func(dep store.Deployment) string {
		raw, _ := os.ReadFile(e.svc.LogPath(dep))
		return string(raw)
	}

	// TCP: the container's name and port are dialed, and the deploy goes through.
	save(store.HealthCheck{Type: store.HealthTCP, Interval: 1, Timeout: 1, Retries: 1})
	good := e.deploy()
	if good.Status != store.DeploySucceeded {
		t.Fatalf("tcp check: %+v\n%s", good, logOf(good))
	}
	if want := good.ContainerName + ":80"; len(dialed) == 0 || dialed[0] != want {
		t.Errorf("dialed %v, want %s", dialed, want)
	}
	if !strings.Contains(logOf(good), "Health check passed (attempt 1") {
		t.Errorf("log has no passed line:\n%s", logOf(good))
	}

	// HTTP: the path is requested, the status compared.
	save(store.HealthCheck{Type: store.HealthHTTP, Path: "/healthz", ExpectedStatus: 204, Interval: 1, Timeout: 1, Retries: 2})
	mu.Lock()
	status = http.StatusNoContent
	mu.Unlock()
	if good = e.deploy(); good.Status != store.DeploySucceeded {
		t.Fatalf("http check: %+v\n%s", good, logOf(good))
	}
	wantRoute := e.router.last(e.app.ID)

	// A wrong status fails after Retries attempts; the old container keeps serving.
	mu.Lock()
	status, hits = http.StatusServiceUnavailable, 0
	mu.Unlock()
	dep := e.deploy()
	if dep.Status != store.DeployFailed || !strings.Contains(dep.Error, "health check failed 2 times in a row: answered 503, expected 204") {
		t.Fatalf("failing check: %+v", dep)
	}
	if hits != 2 {
		t.Errorf("hits = %d, want 2", hits)
	}
	cs := e.containers()
	if len(cs) != 1 || cs[0].Name != good.ContainerName {
		t.Errorf("containers = %+v, want only %s", cs, good.ContainerName)
	}
	if got := e.router.last(e.app.ID); got[0] != wantRoute[0] {
		t.Errorf("route = %v, want untouched %v", got, wantRoute)
	}

	// Expected status 0 takes any 2xx or 3xx, and redirects are not followed.
	save(store.HealthCheck{Type: store.HealthHTTP, Path: "/healthz", Interval: 1, Timeout: 1, Retries: 1})
	mu.Lock()
	status = http.StatusFound
	mu.Unlock()
	if dep := e.deploy(); dep.Status != store.DeploySucceeded {
		t.Fatalf("302 with any 2xx/3xx: %+v\n%s", dep, logOf(dep))
	}

	// A crash fails at once, whatever the check.
	e.setImage("crasher:1")
	e.ex.CrashOnStart["crasher:1"] = true
	if dep := e.deploy(); dep.Status != store.DeployFailed || !strings.Contains(dep.Error, "exited right after start") {
		t.Fatalf("crash under an http check: %+v", dep)
	}
}

// TestHealthCheckStartPeriod: failures during the start period do not count.
func TestHealthCheckStartPeriod(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	var mu sync.Mutex
	calls := 0
	e.svc.opts.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls < 2 { // refused once, then the app is up
			return nil, errors.New("connection refused")
		}
		c1, c2 := net.Pipe()
		c2.Close()
		return c1, nil
	}
	if err := e.st.SetAppHealthCheck(ctx, e.app.ID, store.HealthCheck{Type: store.HealthTCP, Interval: 1, Timeout: 1, Retries: 1, StartPeriod: 5}); err != nil {
		t.Fatal(err)
	}
	dep := e.deploy()
	raw, _ := os.ReadFile(e.svc.LogPath(dep))
	if dep.Status != store.DeploySucceeded || !strings.Contains(string(raw), "Not ready yet: connection refused") {
		t.Fatalf("deployment = %+v\n%s", dep, raw)
	}
}

// TestHealthCheckOutsideNetwork: a panel run on the host cannot reach containers, so a
// saved TCP check falls back to the container staying up.
func TestHealthCheckOutsideNetwork(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	e.svc.opts.OutsideNetwork = true
	e.svc.opts.Dial = func(context.Context, string, string) (net.Conn, error) {
		t.Error("dialed from outside the network")
		return nil, errors.New("no such host")
	}
	if err := e.st.SetAppHealthCheck(ctx, e.app.ID, store.HealthCheck{Type: store.HealthTCP, Interval: 1, Timeout: 1, Retries: 1}); err != nil {
		t.Fatal(err)
	}
	dep := e.deploy()
	raw, _ := os.ReadFile(e.svc.LogPath(dep))
	if dep.Status != store.DeploySucceeded || !strings.Contains(string(raw), "Health check: skipped, the panel runs outside Docker") {
		t.Fatalf("deployment = %+v\n%s", dep, raw)
	}
}

func TestRollbackGit(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	if _, err := e.st.CreateDomain(ctx, e.app.ID, "shop.example.com", ""); err != nil {
		t.Fatal(err)
	}
	e.router.known[e.app.ID] = true
	b := &fakeBuilder{commit: strings.Repeat("a", 40), message: "First version", images: e.ex}
	e.useGit(b)
	first := e.deploy()
	b.commit = strings.Repeat("b", 40)
	second := e.deploy()
	b.buildErr = errors.New("compile error")
	broken := e.deploy()
	b.buildErr = nil
	if first.Status != store.DeploySucceeded || second.Status != store.DeploySucceeded || broken.Status != store.DeployFailed {
		t.Fatalf("setup: %s %s %s", first.Status, second.Status, broken.Status)
	}

	app, _ := e.st.GetApp(ctx, e.app.ID)
	targets, err := e.svc.RollbackTargets(ctx, app, []store.Deployment{first, second, broken})
	if err != nil || !targets[first.ID] || targets[second.ID] || targets[broken.ID] {
		t.Fatalf("targets = %v %v, want only the first", targets, err)
	}
	for _, id := range []string{second.ID, broken.ID} {
		if _, err := e.svc.Rollback(ctx, id); !errors.Is(err, ErrNoRollback) {
			t.Errorf("rollback to %s: %v, want ErrNoRollback", id, err)
		}
	}

	dep, err := e.svc.Rollback(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	dep, _ = e.st.GetDeployment(ctx, dep.ID)
	if dep.Status != store.DeploySucceeded || dep.Trigger != store.TriggerRollback || dep.RollbackOf != first.ID ||
		dep.Image != first.Image || dep.CommitSHA != first.CommitSHA || dep.Port != first.Port {
		t.Fatalf("rollback = %+v", dep)
	}
	if len(b.tags) != 2 {
		t.Errorf("built %v: a rollback must not build", b.tags)
	}
	if dep.CommitMessage != first.CommitMessage {
		t.Errorf("rollback message = %q, want %q", dep.CommitMessage, first.CommitMessage)
	}
	if got := phaseNames(dep); len(got) == 0 || got[0] != store.PhaseStarting {
		t.Errorf("phases = %v: a Git rollback starts at %q", got, store.PhaseStarting)
	}
	if app, _ := e.st.GetApp(ctx, e.app.ID); app.CurrentDeploymentID != dep.ID {
		t.Errorf("current = %s, want the rollback", app.CurrentDeploymentID)
	}
	if cs := e.containers(); len(cs) != 1 || cs[0].Image != first.Image {
		t.Errorf("containers = %+v", cs)
	}
	if got := e.router.last(e.app.ID); len(got) != 1 || !strings.Contains(got[0], dep.ContainerName) {
		t.Errorf("route = %v", got)
	}
	raw, _ := os.ReadFile(e.svc.LogPath(dep))
	if !strings.Contains(string(raw), "Rolling back web to deployment "+first.ID+" (commit "+first.CommitSHA) || strings.Contains(string(raw), "Fetching") {
		t.Errorf("log:\n%s", raw)
	}

	// The second deployment is not live any more: it can be returned to. A pruned image cannot.
	app, _ = e.st.GetApp(ctx, e.app.ID)
	if targets, _ := e.svc.RollbackTargets(ctx, app, []store.Deployment{first, second}); !targets[second.ID] || targets[first.ID] {
		t.Errorf("targets after rollback = %v", targets)
	}
	images, _ := e.ex.ListBuildImages(ctx, e.app.ID)
	for _, img := range images {
		if slices.Contains(img.Tags, second.Image) {
			if err := e.ex.RemoveImage(ctx, img.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := e.svc.Rollback(ctx, second.ID); !errors.Is(err, ErrNoRollback) {
		t.Errorf("rollback to a pruned image: %v", err)
	}
}

func TestRollbackImage(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	first := e.deploy()
	e.setImage("nginx:2")
	if dep := e.deploy(); dep.Status != store.DeploySucceeded {
		t.Fatal(dep)
	}
	dep, err := e.svc.Rollback(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
	dep, _ = e.st.GetDeployment(ctx, dep.ID)
	raw, _ := os.ReadFile(e.svc.LogPath(dep))
	if dep.Status != store.DeploySucceeded || dep.Image != "nginx:1" || !strings.Contains(string(raw), "Pulling image nginx:1") {
		t.Fatalf("rollback = %+v\n%s", dep, raw)
	}
	// The app's settings follow, so a redeploy stays on the rolled-back image.
	if app, _ := e.st.GetApp(ctx, e.app.ID); app.Image != "nginx:1" {
		t.Errorf("app image = %s, want nginx:1", app.Image)
	}
	if !strings.Contains(string(raw), "The app's image is now nginx:1") {
		t.Errorf("log:\n%s", raw)
	}
}

// phaseNames lists the phases a deployment went through, the closing status included.
func phaseNames(d store.Deployment) []string {
	out := make([]string, len(d.Phases))
	for i, p := range d.Phases {
		out[i] = p.Phase
	}
	return out
}

func TestDeployPhases(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	if _, err := e.st.CreateDomain(ctx, e.app.ID, "shop.example.com", ""); err != nil {
		t.Fatal(err)
	}
	e.router.known[e.app.ID] = true

	dep := e.deploy()
	want := []string{store.PhasePulling, store.PhaseStarting, store.PhaseHealthChecking, store.PhaseSwitching, store.PhaseDraining, store.DeploySucceeded}
	if got := phaseNames(dep); !slices.Equal(got, want) {
		t.Fatalf("phases = %v, want %v", got, want)
	}
	if dep.Phase != store.PhaseDraining {
		t.Errorf("phase = %q, want %q", dep.Phase, store.PhaseDraining)
	}
	for i := 1; i < len(dep.Phases); i++ {
		if dep.Phases[i].At.Before(dep.Phases[i-1].At) {
			t.Errorf("phase %d starts before phase %d: %+v", i, i-1, dep.Phases)
		}
	}

	// A failure ends the timeline in the phase it failed in.
	e.setImage("crasher:1")
	e.ex.CrashOnStart["crasher:1"] = true
	dep = e.deploy()
	if dep.Status != store.DeployFailed || dep.Phase != store.PhaseHealthChecking {
		t.Errorf("crash: status %q phase %q, want failed in %q", dep.Status, dep.Phase, store.PhaseHealthChecking)
	}
	if got := phaseNames(dep); !slices.Equal(got, []string{store.PhasePulling, store.PhaseStarting, store.PhaseHealthChecking, store.DeployFailed}) {
		t.Errorf("crash: phases = %v", got)
	}

	e.setImage("registry.example/missing:1")
	if dep = e.deploy(); dep.Phase != store.PhasePulling {
		t.Errorf("missing image: phase %q, want %q", dep.Phase, store.PhasePulling)
	}

	e.setImage("nginx:3")
	e.router.failAll = true
	if dep = e.deploy(); dep.Phase != store.PhaseSwitching {
		t.Errorf("proxy down: phase %q, want %q", dep.Phase, store.PhaseSwitching)
	}
}

func TestDeployPhasesGit(t *testing.T) {
	e := newEnv(t)
	b := &fakeBuilder{commit: strings.Repeat("cd34", 10), message: "Fix the login page\n\nThe form posted twice."}
	e.useGit(b)
	dep := e.deploy()
	if dep.CommitMessage != b.message {
		t.Errorf("commit message = %q, want %q", dep.CommitMessage, b.message)
	}
	if !strings.Contains(e.log(dep), "Commit "+b.commit+": Fix the login page") {
		t.Errorf("the log does not name the commit's subject:\n%s", e.log(dep))
	}
	want := []string{store.PhaseBuilding, store.PhaseStarting, store.PhaseHealthChecking, store.PhaseSwitching, store.PhaseDraining, store.DeploySucceeded}
	if got := phaseNames(dep); dep.Status != store.DeploySucceeded || !slices.Equal(got, want) {
		t.Fatalf("status %q, phases = %v, want %v", dep.Status, got, want)
	}
}
