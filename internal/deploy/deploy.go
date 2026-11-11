// Package deploy turns an app's settings into a running container and keeps the proxy
// pointed at it. This is the minimal pipeline: pull (or, for a Git app, fetch and build),
// start, switch the route, remove the old container. Health checks, draining and rollback
// build on it later.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"panel/internal/builder"
	"panel/internal/executor"
	"panel/internal/store"
)

var (
	// ErrBusy means another operation on the same app is still in progress.
	ErrBusy = errors.New("another operation on this app is in progress")
	// ErrNotDeployed means the app has no container to act on yet.
	ErrNotDeployed = errors.New("the app has not been deployed yet")
)

// Router is the part of the proxy a deploy needs.
type Router interface {
	// Apply rebuilds the whole proxy configuration from the database.
	Apply(ctx context.Context) error
	// SetUpstreams switches one route atomically.
	SetUpstreams(ctx context.Context, routeID string, upstreams []string) error
}

// Builder fetches and builds Git apps; *builder.Local implements it.
type Builder interface {
	// Fetch clones the branch tip into dir and returns the commit.
	Fetch(ctx context.Context, src store.GitSource, auth builder.Auth, dir string, line builder.LineFunc) (string, error)
	// Build builds dir into the local image tag.
	Build(ctx context.Context, src store.GitSource, dir, tag, appID string, line builder.LineFunc) error
}

// GitAuth hands out credentials for private repositories; *gitsource.Service implements it.
type GitAuth interface {
	Credentials(ctx context.Context, connectionID, repository string) (builder.Auth, error)
}

type Options struct {
	Store    *store.Store
	Executor executor.Executor
	Router   Router  // nil: no proxy, routes are not touched
	Builder  Builder // nil: Git apps cannot be deployed
	GitAuth  GitAuth // nil: only public repositories
	DataDir  string  // deployment logs live in <DataDir>/deployments, build workspaces in <DataDir>/builds
	Log      *slog.Logger
	// StartGrace is how long a new container must stay up before traffic is switched to
	// it. It catches images that crash on start; real health checks replace it later.
	StartGrace time.Duration
	// StopGrace is how long a container gets to exit before it is killed.
	StopGrace time.Duration
}

type Service struct {
	opts Options
	ctx  context.Context // lifetime of background deploys

	mu   sync.Mutex
	busy map[string]bool // app ids with an operation in progress
	wg   sync.WaitGroup
}

// New returns a Service whose background deploys stop when ctx is cancelled.
func New(ctx context.Context, opts Options) *Service {
	if opts.StartGrace == 0 {
		opts.StartGrace = 3 * time.Second
	}
	if opts.StopGrace == 0 {
		opts.StopGrace = 10 * time.Second
	}
	return &Service{opts: opts, ctx: ctx, busy: map[string]bool{}}
}

// Wait blocks until every background deploy has finished.
func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) acquire(appID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy[appID] {
		return false
	}
	s.busy[appID] = true
	return true
}

func (s *Service) release(appID string) {
	s.mu.Lock()
	delete(s.busy, appID)
	s.mu.Unlock()
}

// Recover closes deployments a previous panel process left unfinished. Call it once at
// startup, before any new deploy.
func (s *Service) Recover(ctx context.Context) (int, error) {
	// Workspaces of builds the previous process did not finish.
	if err := os.RemoveAll(filepath.Join(s.opts.DataDir, "builds")); err != nil {
		s.opts.Log.Error("remove old build workspaces", "error", err)
	}
	return s.opts.Store.FailUnfinishedDeployments(ctx, "interrupted: the panel restarted during this deployment")
}

// Deploy records a deployment and runs it in the background. The returned record is
// queued; poll it (or read its log) to follow progress.
func (s *Service) Deploy(ctx context.Context, appID, trigger string) (store.Deployment, error) {
	app, err := s.opts.Store.GetApp(ctx, appID)
	if err != nil {
		return store.Deployment{}, err
	}
	if !s.acquire(appID) {
		return store.Deployment{}, ErrBusy
	}
	dep, err := s.opts.Store.CreateDeployment(ctx, appID, trigger, app.Image, app.Port, func(id string) string {
		return filepath.Join("deployments", appID, id+".log")
	})
	if err != nil {
		s.release(appID)
		return store.Deployment{}, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.release(appID)
		s.run(app, dep)
	}()
	return dep, nil
}

// run is one deployment from start to finish. Whatever happens, the record ends up
// succeeded or failed, and a failed attempt leaves the previous container serving.
func (s *Service) run(app store.App, dep store.Deployment) {
	st, ex := s.opts.Store, s.opts.Executor
	ctx := s.ctx
	// Bookkeeping must still be written when ctx is cancelled (shutdown mid-deploy).
	final := context.WithoutCancel(ctx)

	logw := s.openLog(dep)
	defer logw.close()

	fail := func(containerID string, err error) {
		msg := err.Error()
		if ctx.Err() != nil {
			msg = "cancelled: the panel is shutting down"
		}
		logw.printf("Deployment failed: %s", msg)
		if containerID != "" {
			if rmErr := ex.RemoveContainer(final, containerID, true); rmErr != nil {
				s.opts.Log.Error("remove failed deployment container", "error", rmErr)
			}
		}
		if err := st.FinishDeployment(final, dep.ID, store.DeployFailed, msg); err != nil {
			s.opts.Log.Error("finish deployment", "error", err)
		}
		s.event(final, "deployment.failed", app, msg)
		s.opts.Log.Warn("deployment failed", "app", app.Name, "deployment", dep.ID, "error", msg)
	}

	if err := st.MarkDeploymentRunning(final, dep.ID); err != nil {
		fail("", err)
		return
	}
	// 1. Pull the image, or build it from Git.
	image := app.Image
	if app.SourceType == store.SourceGit {
		logw.printf("Deploying %s from %s (branch %s)", app.Name, app.Git.Repository, app.Git.Branch)
		var err error
		if image, err = s.build(ctx, final, app, dep, logw); err != nil {
			fail("", err)
			return
		}
	} else {
		logw.printf("Deploying %s (image %s)", app.Name, app.Image)
		if err := s.pull(ctx, image, logw); err != nil {
			fail("", err)
			return
		}
	}

	// 2. Create and start the new container next to the old one.
	name := containerName(app.ID, dep.ID)
	logw.printf("Creating container %s", name)
	containerID, err := ex.CreateContainer(ctx, executor.ContainerSpec{
		Name:       name,
		Image:      image,
		Kind:       executor.KindApp,
		ResourceID: app.ID,
		Labels:     map[string]string{"deployment": dep.ID},
		CPUs:       app.CPULimit,
		MemoryMB:   app.MemoryMB,
		Env:        app.Environment,
	})
	if err != nil {
		fail("", err)
		return
	}
	if err := st.SetDeploymentContainer(final, dep.ID, containerID, name); err != nil {
		fail(containerID, err)
		return
	}
	logw.printf("Starting container")
	if err := ex.StartContainer(ctx, containerID); err != nil {
		fail(containerID, err)
		return
	}
	if err := s.waitStable(ctx, containerID, logw); err != nil {
		fail(containerID, err)
		return
	}

	// 3. Switch: the database first (it is the source of truth), then the proxy.
	previous := app.CurrentDeploymentID
	if err := st.SetAppCurrentDeployment(final, app.ID, dep.ID, store.StateRunning); err != nil {
		fail(containerID, err)
		return
	}
	logw.printf("Switching traffic to the new container")
	if err := s.route(ctx, app.ID, []string{store.Upstream(name, dep.Port)}); err != nil {
		// Put the database back so it matches what the proxy still serves.
		if rbErr := st.SetAppCurrentDeployment(final, app.ID, previous, app.DesiredState); rbErr != nil {
			s.opts.Log.Error("revert current deployment", "error", rbErr)
		}
		fail(containerID, fmt.Errorf("switch route: %w", err))
		return
	}

	// 4. Remove every other container of this app. From here the deploy has succeeded;
	// a leftover container is logged, not fatal, and the next deploy removes it.
	if list, err := ex.ListContainers(final); err != nil {
		logw.printf("Warning: could not list old containers: %v", err)
	} else {
		for _, c := range list {
			if c.Kind != executor.KindApp || c.ResourceID != app.ID || c.ID == containerID {
				continue
			}
			logw.printf("Removing old container %s", c.Name)
			if c.Running {
				if err := ex.StopContainer(final, c.ID, s.opts.StopGrace); err != nil {
					logw.printf("Warning: stop %s: %v", c.Name, err)
				}
			}
			if err := ex.RemoveContainer(final, c.ID, true); err != nil {
				logw.printf("Warning: remove %s: %v", c.Name, err)
			}
		}
	}

	// The last log line goes in before the status changes: a log stream stops reading
	// once it sees a finished status, and must have the whole file by then.
	logw.printf("Deployment succeeded")
	if err := st.FinishDeployment(final, dep.ID, store.DeploySucceeded, ""); err != nil {
		s.opts.Log.Error("finish deployment", "error", err)
	}
	s.event(final, "deployment.succeeded", app, "")
	s.opts.Log.Info("deployment succeeded", "app", app.Name, "deployment", dep.ID)
}

// pull pulls an image, logging one line per layer per status change.
func (s *Service) pull(ctx context.Context, image string, logw *logWriter) error {
	logw.printf("Pulling image %s", image)
	last := map[string]string{}
	return s.opts.Executor.PullImage(ctx, image, func(p executor.PullProgress) {
		// One line per layer per status change, not one per progress tick.
		if p.Status == "" || last[p.Layer] == p.Status {
			return
		}
		last[p.Layer] = p.Status
		if p.Layer == "" {
			logw.printf("  %s", p.Status)
		} else {
			logw.printf("  %s: %s", p.Layer, p.Status)
		}
	})
}

// build fetches a Git app at its branch tip and builds it into a local image, which it
// returns. The workspace is removed afterwards, whatever happens.
func (s *Service) build(ctx, final context.Context, app store.App, dep store.Deployment, logw *logWriter) (string, error) {
	if s.opts.Builder == nil {
		return "", errors.New("this panel cannot build from Git")
	}
	st, src := s.opts.Store, app.Git
	dir := filepath.Join(s.opts.DataDir, "builds", dep.ID)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", err
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			s.opts.Log.Error("remove build workspace", "error", err)
		}
	}()
	out := func(l string) { logw.printf("  | %s", l) }

	var auth builder.Auth
	if src.ConnectionID != "" {
		if s.opts.GitAuth == nil {
			return "", errors.New("this panel cannot fetch private repositories")
		}
		var err error
		if auth, err = s.opts.GitAuth.Credentials(ctx, src.ConnectionID, src.Repository); err != nil {
			return "", fmt.Errorf("git connection: %w", err)
		}
	}
	logw.printf("Fetching branch %s", src.Branch)
	commit, err := s.opts.Builder.Fetch(ctx, src, auth, dir, out)
	if err != nil {
		return "", err
	}
	logw.printf("Commit %s", commit)
	if err := st.SetDeploymentCommit(final, dep.ID, commit); err != nil {
		return "", err
	}

	tag := imageTag(app.ID, commit)
	logw.printf("Building image %s (builder: %s)", tag, src.Builder)
	if err := s.opts.Builder.Build(ctx, src, dir, tag, app.ID, out); err != nil {
		return "", err
	}
	if err := st.SetDeploymentImage(final, dep.ID, tag); err != nil {
		return "", err
	}
	logw.printf("Built image %s", tag)
	return tag, nil
}

// imageTag names the image built from one commit of an app: "app-<app id>:<commit>".
func imageTag(appID, commit string) string {
	return strings.ToLower("app-"+appID) + ":" + commit
}

// waitStable gives the container StartGrace to prove it does not crash on start. Under
// the restart policy a crashing container is restarted at once, so "running" alone proves
// nothing: a restart, or the "restarting" state, counts as a crash too.
func (s *Service) waitStable(ctx context.Context, containerID string, logw *logWriter) error {
	deadline := time.Now().Add(s.opts.StartGrace)
	for {
		c, err := s.opts.Executor.InspectContainer(ctx, containerID)
		if err != nil {
			return err
		}
		if !c.Running || c.State == "restarting" || c.Restarts > 0 {
			var lines []string
			_ = s.opts.Executor.Logs(ctx, containerID, executor.LogOptions{Tail: 20}, func(l executor.LogLine) {
				lines = append(lines, l.Text)
				logw.printf("  | %s", l.Text)
			})
			reason := fmt.Sprintf("the container exited right after start (exit code %d)", c.ExitCode)
			if c.OOMKilled {
				reason = "the container was killed for exceeding its memory limit"
			}
			if len(lines) > 0 {
				reason += ": " + lines[len(lines)-1]
			}
			return errors.New(reason)
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(250*time.Millisecond, s.opts.StartGrace)):
		}
	}
}

// route points the app's hostnames at upstreams. Apps without domains have no route.
// The atomic single-route patch is tried first; a full rebuild is the fallback (for
// example when the route is not in the running configuration yet).
func (s *Service) route(ctx context.Context, appID string, upstreams []string) error {
	if s.opts.Router == nil {
		return nil
	}
	domains, err := s.opts.Store.ListDomains(ctx, appID)
	if err != nil {
		return err
	}
	if len(domains) == 0 {
		return nil
	}
	if err := s.opts.Router.SetUpstreams(ctx, appID, upstreams); err == nil {
		return nil
	}
	return s.opts.Router.Apply(ctx)
}

// current returns the app and the deployment it is serving from.
func (s *Service) current(ctx context.Context, appID string) (store.App, store.Deployment, error) {
	app, err := s.opts.Store.GetApp(ctx, appID)
	if err != nil {
		return store.App{}, store.Deployment{}, err
	}
	if app.CurrentDeploymentID == "" {
		return app, store.Deployment{}, ErrNotDeployed
	}
	dep, err := s.opts.Store.GetDeployment(ctx, app.CurrentDeploymentID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && dep.ContainerName == "") {
		return app, store.Deployment{}, ErrNotDeployed
	}
	return app, dep, err
}

// Stop takes the app offline: its hostnames answer 503 and the container is stopped, but
// kept, so Start is instant.
func (s *Service) Stop(ctx context.Context, appID string) error {
	if !s.acquire(appID) {
		return ErrBusy
	}
	defer s.release(appID)
	app, dep, err := s.current(ctx, appID)
	if err != nil {
		return err
	}
	if err := s.opts.Store.SetAppDesiredState(ctx, app.ID, store.StateStopped); err != nil {
		return err
	}
	if err := s.route(ctx, app.ID, nil); err != nil {
		s.opts.Log.Error("clear route", "app", app.Name, "error", err)
	}
	err = s.opts.Executor.StopContainer(ctx, dep.ContainerName, s.opts.StopGrace)
	if errors.Is(err, executor.ErrNotFound) {
		return nil // already gone: stopped is what was asked for
	}
	return err
}

// Start brings a stopped app back on the container of its current deployment.
func (s *Service) Start(ctx context.Context, appID string) error {
	if !s.acquire(appID) {
		return ErrBusy
	}
	defer s.release(appID)
	app, dep, err := s.current(ctx, appID)
	if err != nil {
		return err
	}
	if err := s.opts.Executor.StartContainer(ctx, dep.ContainerName); err != nil {
		if errors.Is(err, executor.ErrNotFound) {
			return ErrNotDeployed // the container was removed behind our back: deploy again
		}
		return err
	}
	if err := s.opts.Store.SetAppDesiredState(ctx, app.ID, store.StateRunning); err != nil {
		return err
	}
	return s.route(ctx, app.ID, []string{store.Upstream(dep.ContainerName, dep.Port)})
}

// Restart stops and starts the current container. There is a short gap; a deploy is the
// zero-downtime way to replace a container.
func (s *Service) Restart(ctx context.Context, appID string) error {
	if !s.acquire(appID) {
		return ErrBusy
	}
	defer s.release(appID)
	app, dep, err := s.current(ctx, appID)
	if err != nil {
		return err
	}
	ex := s.opts.Executor
	if err := ex.StopContainer(ctx, dep.ContainerName, s.opts.StopGrace); err != nil {
		if errors.Is(err, executor.ErrNotFound) {
			return ErrNotDeployed
		}
		return err
	}
	if err := ex.StartContainer(ctx, dep.ContainerName); err != nil {
		return err
	}
	if err := s.opts.Store.SetAppDesiredState(ctx, app.ID, store.StateRunning); err != nil {
		return err
	}
	return s.route(ctx, app.ID, []string{store.Upstream(dep.ContainerName, dep.Port)})
}

// Remove deletes everything an app has outside the database: its containers and its
// deployment logs. The caller deletes the database rows and rebuilds the proxy.
func (s *Service) Remove(ctx context.Context, appID string) error {
	if !s.acquire(appID) {
		return ErrBusy
	}
	defer s.release(appID)
	list, err := s.opts.Executor.ListContainers(ctx)
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.Kind == executor.KindApp && c.ResourceID == appID {
			if err := s.opts.Executor.RemoveContainer(ctx, c.ID, true); err != nil {
				return err
			}
		}
	}
	return os.RemoveAll(filepath.Join(s.opts.DataDir, "deployments", appID))
}

// RuntimeState reports what the app's container is doing right now:
// running, exited, restarting, …; "missing" when it is gone; "none" before the first deploy.
func (s *Service) RuntimeState(ctx context.Context, appID string) string {
	_, dep, err := s.current(ctx, appID)
	if errors.Is(err, ErrNotDeployed) {
		return "none"
	}
	if err != nil {
		return "unknown"
	}
	c, err := s.opts.Executor.InspectContainer(ctx, dep.ContainerName)
	switch {
	case errors.Is(err, executor.ErrNotFound):
		return "missing"
	case err != nil:
		return "unknown"
	}
	return c.State
}

// StreamLogs calls fn for each log line of the app's current container. With
// opts.Follow it returns when the container stops or ctx is cancelled.
func (s *Service) StreamLogs(ctx context.Context, appID string, opts executor.LogOptions, fn func(executor.LogLine)) error {
	_, dep, err := s.current(ctx, appID)
	if err != nil {
		return err
	}
	err = s.opts.Executor.Logs(ctx, dep.ContainerName, opts, fn)
	if errors.Is(err, executor.ErrNotFound) {
		return ErrNotDeployed
	}
	if ctx.Err() != nil {
		return nil // the reader went away: not a failure
	}
	return err
}

// LogPath is the absolute path of a deployment's log file.
func (s *Service) LogPath(dep store.Deployment) string {
	return filepath.Join(s.opts.DataDir, dep.LogPath)
}

func (s *Service) event(ctx context.Context, action string, app store.App, detail string) {
	err := s.opts.Store.AddEvent(ctx, store.Event{
		Actor: "system", Action: action, TargetType: "app", TargetID: app.ID, TargetName: app.Name, Detail: detail,
	})
	if err != nil {
		s.opts.Log.Error("record event", "error", err)
	}
}

// containerName is unique per deployment and a valid DNS label, since the proxy reaches
// the container by this name: "app-<app id>-<last 6 of the deployment id>".
func containerName(appID, deploymentID string) string {
	suffix := deploymentID
	if len(suffix) > 6 {
		suffix = suffix[len(suffix)-6:]
	}
	return strings.ToLower("app-" + appID + "-" + suffix)
}

// logWriter appends timestamped lines to a deployment's log file. A log that cannot be
// written must never fail a deployment, so errors are dropped.
type logWriter struct{ f *os.File }

func (s *Service) openLog(dep store.Deployment) *logWriter {
	path := s.LogPath(dep)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		s.opts.Log.Error("deployment log", "error", err)
		return &logWriter{}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		s.opts.Log.Error("deployment log", "error", err)
		return &logWriter{}
	}
	return &logWriter{f: f}
}

func (w *logWriter) printf(format string, args ...any) {
	if w.f == nil {
		return
	}
	fmt.Fprintf(w.f, "%s %s\n", time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), fmt.Sprintf(format, args...))
}

func (w *logWriter) close() {
	if w.f != nil {
		w.f.Close()
	}
}
