// Package deploy turns an app's settings into a running container and keeps the proxy
// pointed at it. A deployment is a state machine: queued → pulling (or, for a Git app,
// building) → starting → health_checking → switching → draining → succeeded, leaving
// for failed or cancelled at any step before switching. Each step entered is recorded
// with its time (store.EnterDeploymentPhase), for the timeline.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	// ErrNotCancellable means the deployment has finished, or is already switching traffic.
	ErrNotCancellable = errors.New("the deployment can no longer be cancelled")
	// ErrNoRollback means the deployment cannot be returned to: it did not succeed, it is
	// the live one, or its built image was pruned.
	ErrNoRollback = errors.New("this deployment cannot be rolled back to")

	errCancelled = errors.New("cancelled")
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
	DataVolume string
	Store      *store.Store
	Executor   executor.Executor
	Router     Router  // nil: no proxy, routes are not touched
	Builder    Builder // nil: Git apps cannot be deployed
	GitAuth    GitAuth // nil: only public repositories
	DataDir    string  // deployment logs live in <DataDir>/deployments, build workspaces in <DataDir>/builds
	Log        *slog.Logger
	// StartGrace is how long a new container must stay up before traffic is switched to
	// it, for an app with no health check saved. It catches images that crash on start.
	StartGrace time.Duration
	// Dial opens the connections of health checks; nil = a plain net.Dialer. The panel
	// shares a Docker network with the apps, so it reaches a container by its name.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// OutsideNetwork says the panel runs straight on the host (development), off the
	// apps' Docker network: TCP and HTTP checks cannot reach a container, so a deploy only
	// waits StartGrace for the container to stay up, and says so in its log.
	OutsideNetwork bool
	// StopGrace is how long a container gets to exit before it is killed.
	StopGrace time.Duration
	// MaxBuilds is how many Git builds run at once; 0 = 1. Further Git deploys stay queued
	// until a build slot frees. Image pulls do not take a slot.
	MaxBuilds int
	// MaxAttempts is how many times a deployment may start; 0 = 3. A deployment the panel
	// restarted under starts again from the beginning, up to this many times in all.
	MaxAttempts int
	// RetryDelays are the waits before each retry of a step that failed for a temporary
	// reason (network) — a clone, a pull; nil = 2 s then 5 s. Its length is the retry count.
	RetryDelays []time.Duration
	// Ready, when set, holds back the deployments Recover resumes until it is closed — the
	// proxy is up, so their traffic switch can work. New deploys do not wait.
	Ready <-chan struct{}
}

type Service struct {
	opts Options
	ctx  context.Context // lifetime of background deploys

	builds chan struct{} // one token per running build

	mu      sync.Mutex
	busy    map[string]bool         // app ids with an operation in progress
	pending map[string]string       // app id → trigger of one deploy to start once the app is free
	running map[string]*cancellable // unfinished deployments by id
	wg      sync.WaitGroup
}

// cancellable is the cancel hook of an unfinished deployment. Once the deployment starts
// switching traffic, cancelling would leave the proxy half-changed, so it is refused.
type cancellable struct {
	cancel    context.CancelCauseFunc
	switching bool
}

// New returns a Service whose background deploys stop when ctx is cancelled.
func New(ctx context.Context, opts Options) *Service {
	if opts.StartGrace == 0 {
		opts.StartGrace = 3 * time.Second
	}
	if opts.Dial == nil {
		opts.Dial = (&net.Dialer{}).DialContext
	}
	if opts.StopGrace == 0 {
		opts.StopGrace = 10 * time.Second
	}
	if opts.MaxBuilds < 1 {
		opts.MaxBuilds = 1
	}
	if opts.MaxAttempts < 1 {
		opts.MaxAttempts = 3
	}
	if opts.RetryDelays == nil {
		opts.RetryDelays = []time.Duration{2 * time.Second, 5 * time.Second}
	}
	return &Service{opts: opts, ctx: ctx, busy: map[string]bool{}, pending: map[string]string{}, running: map[string]*cancellable{},
		builds: make(chan struct{}, opts.MaxBuilds)}
}

// Cancel stops an unfinished deployment: a queued one never starts, a running one is
// interrupted (the build or pull is killed, a new container removed) and the previous
// container keeps serving. It ends with status cancelled. Asynchronous: the deployment
// record changes when the run notices, usually within a second.
func (s *Service) Cancel(deploymentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.running[deploymentID]
	if c == nil || c.switching {
		return ErrNotCancellable
	}
	c.cancel(errCancelled)
	return nil
}

// enterSwitch marks the deployment as no longer cancellable. It reports false when a
// cancel arrived first.
func (s *Service) enterSwitch(deploymentID string, ctx context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	if c := s.running[deploymentID]; c != nil {
		c.switching = true
	}
	return true
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

// release frees the app, and starts the deploy queued for it meanwhile, if any.
func (s *Service) release(appID string) {
	s.mu.Lock()
	delete(s.busy, appID)
	trigger, again := s.pending[appID]
	delete(s.pending, appID)
	s.mu.Unlock()
	if !again || s.ctx.Err() != nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if _, err := s.Deploy(s.ctx, appID, trigger); err != nil && !errors.Is(err, ErrBusy) {
			s.opts.Log.Warn("queued deploy did not start", "app", appID, "error", err)
		}
	}()
}

// DeployOrQueue deploys like Deploy, but when another operation on the app is in
// progress it queues one deploy to start once that ends, and reports queued. Several
// calls meanwhile still make one deploy: it builds what is current when it starts, so a
// burst of pushes ships the last one.
func (s *Service) DeployOrQueue(ctx context.Context, appID, trigger string) (store.Deployment, bool, error) {
	for {
		dep, err := s.Deploy(ctx, appID, trigger)
		if !errors.Is(err, ErrBusy) {
			return dep, false, err
		}
		s.mu.Lock()
		if s.busy[appID] {
			s.pending[appID] = trigger
			s.mu.Unlock()
			return store.Deployment{}, true, nil
		}
		s.mu.Unlock() // freed in between: try again
	}
}

// Recovery is what Recover did with the deployments a previous panel process left.
type Recovery struct {
	Resumed  int // queued again and started
	Finished int // had already switched traffic: completed
	Failed   int // started too many times, or their app is gone
}

// Recover picks up the deployments a previous panel process left unfinished. Call it once
// at startup, before any new deploy. A queued deployment starts. A running one that had
// not switched traffic yet starts again from the beginning, its half-made container
// removed — unless it has already started MaxAttempts times, then it fails. A running one
// that had switched traffic is completed: the old containers go, it is marked succeeded.
func (s *Service) Recover(ctx context.Context) (Recovery, error) {
	var r Recovery
	// Workspaces of builds the previous process did not finish.
	if err := os.RemoveAll(filepath.Join(s.opts.DataDir, "builds")); err != nil {
		s.opts.Log.Error("remove old build workspaces", "error", err)
	}
	st := s.opts.Store
	deps, err := st.ListUnfinishedDeployments(ctx)
	if err != nil {
		return r, err
	}
	for _, dep := range deps {
		app, err := st.GetApp(ctx, dep.AppID)
		if err != nil {
			if ferr := st.FinishDeployment(ctx, dep.ID, store.DeployFailed, "interrupted: the panel restarted during this deployment"); ferr != nil {
				return r, ferr
			}
			r.Failed++
			continue
		}
		logw := s.openLog(dep)
		if dep.Status == store.DeployRunning && app.CurrentDeploymentID == dep.ID {
			// Traffic had switched to the new container; only the cleanup was left.
			logw.printf("The panel restarted after switching traffic; finishing")
			if err := st.EnterDeploymentPhase(ctx, dep.ID, store.PhaseDraining); err != nil {
				s.opts.Log.Error("record deployment phase", "deployment", dep.ID, "error", err)
			}
			s.removeOthers(ctx, app, dep.ContainerID, logw)
			logw.printf("Deployment succeeded")
			logw.close()
			if err := st.FinishDeployment(ctx, dep.ID, store.DeploySucceeded, ""); err != nil {
				return r, err
			}
			s.event(ctx, "deployment.succeeded", app, "")
			r.Finished++
			continue
		}
		if dep.Status == store.DeployRunning {
			// The interrupted attempt may have created its container already.
			if err := s.opts.Executor.RemoveContainer(ctx, containerName(app.ID, dep.ID), true); err != nil {
				s.opts.Log.Error("remove interrupted deployment container", "deployment", dep.ID, "error", err)
			}
		}
		if dep.Attempts >= s.opts.MaxAttempts {
			msg := fmt.Sprintf("interrupted: the panel restarted during this deployment %d times", dep.Attempts)
			logw.printf("Deployment failed: %s", msg)
			logw.close()
			if err := st.FinishDeployment(ctx, dep.ID, store.DeployFailed, msg); err != nil {
				return r, err
			}
			s.event(ctx, "deployment.failed", app, msg)
			r.Failed++
			continue
		}
		logw.close()
		if err := st.RequeueDeployment(ctx, dep.ID); err != nil {
			return r, err
		}
		if dep, err = st.GetDeployment(ctx, dep.ID); err != nil {
			return r, err
		}
		if !s.acquire(app.ID) {
			continue // cannot happen: one unfinished deployment per app, and nothing else runs yet
		}
		s.start(app, dep, s.opts.Ready)
		r.Resumed++
	}
	return r, nil
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
	dep, err := s.opts.Store.CreateDeployment(ctx, appID, trigger, app.Image, app.Port, logPath(appID))
	if err != nil {
		s.release(appID)
		return store.Deployment{}, err
	}
	s.start(app, dep, nil)
	return dep, nil
}

func logPath(appID string) func(deploymentID string) string {
	return func(id string) string { return filepath.Join("deployments", appID, id+".log") }
}

// Rollback deploys the image of an earlier deployment again, through the same pipeline
// (start, health check, switch) but without a build. A Git app can return to the images
// still kept (KeepImages); an image app pulls the image again. The returned record is
// queued, like Deploy's.
func (s *Service) Rollback(ctx context.Context, deploymentID string) (store.Deployment, error) {
	src, err := s.opts.Store.GetDeployment(ctx, deploymentID)
	if err != nil {
		return store.Deployment{}, err
	}
	app, err := s.opts.Store.GetApp(ctx, src.AppID)
	if err != nil {
		return store.Deployment{}, err
	}
	targets, err := s.RollbackTargets(ctx, app, []store.Deployment{src})
	if err != nil {
		return store.Deployment{}, err
	}
	if !targets[src.ID] {
		return store.Deployment{}, ErrNoRollback
	}
	if !s.acquire(app.ID) {
		return store.Deployment{}, ErrBusy
	}
	dep, err := s.opts.Store.CreateRollbackDeployment(ctx, src, logPath(app.ID))
	if err != nil {
		s.release(app.ID)
		return store.Deployment{}, err
	}
	s.start(app, dep, nil)
	return dep, nil
}

// RollbackTargets tells which of deps (deployments of app) a rollback can return to: the
// succeeded ones whose image is not the live one, and for a Git app only those whose image
// is still kept.
func (s *Service) RollbackTargets(ctx context.Context, app store.App, deps []store.Deployment) (map[string]bool, error) {
	var kept map[string]bool
	if app.SourceType == store.SourceGit {
		images, err := s.opts.Executor.ListBuildImages(ctx, app.ID)
		if err != nil {
			return nil, err
		}
		kept = map[string]bool{}
		for _, img := range images {
			for _, tag := range img.Tags {
				kept[tag] = true
			}
		}
	}
	live := ""
	if app.CurrentDeploymentID != "" {
		cur, err := s.opts.Store.GetDeployment(ctx, app.CurrentDeploymentID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		live = cur.Image
	}
	out := map[string]bool{}
	for _, d := range deps {
		ok := d.AppID == app.ID && d.Status == store.DeploySucceeded && d.Image != "" && d.ID != app.CurrentDeploymentID && d.Image != live
		if ok && kept != nil {
			ok = kept[d.Image]
		}
		if ok {
			out[d.ID] = true
		}
	}
	return out, nil
}

// start runs dep in the background, once ready is closed (nil: at once). The caller holds
// the app (acquire); it is released when the deployment ends. Shut down before ready, the
// deployment stays queued.
func (s *Service) start(app store.App, dep store.Deployment, ready <-chan struct{}) {
	ctx, cancel := context.WithCancelCause(s.ctx)
	s.mu.Lock()
	s.running[dep.ID] = &cancellable{cancel: cancel}
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.release(app.ID)
		defer func() {
			s.mu.Lock()
			delete(s.running, dep.ID)
			s.mu.Unlock()
			cancel(nil)
		}()
		if ready != nil {
			select {
			case <-ready:
			case <-ctx.Done():
				if errors.Is(context.Cause(ctx), errCancelled) {
					s.run(ctx, app, dep) // records the cancel
				}
				return
			}
		}
		s.run(ctx, app, dep)
	}()
}

// run is one deployment from start to finish. Whatever happens, the record ends up
// succeeded or failed, and a failed attempt leaves the previous container serving.
func (s *Service) run(ctx context.Context, app store.App, dep store.Deployment) {
	st, ex := s.opts.Store, s.opts.Executor
	// Bookkeeping must still be written when ctx is cancelled (shutdown mid-deploy).
	final := context.WithoutCancel(ctx)

	logw := s.openLog(dep)
	defer logw.close()
	if dep.Attempts > 0 {
		logw.printf("The panel restarted during this deployment; starting again (attempt %d of %d)", dep.Attempts+1, s.opts.MaxAttempts)
	}

	fail := func(containerID string, err error) {
		if containerID != "" {
			if rmErr := ex.RemoveContainer(final, containerID, true); rmErr != nil {
				s.opts.Log.Error("remove failed deployment container", "error", rmErr)
			}
		}
		msg, status, action := err.Error(), store.DeployFailed, "deployment.failed"
		switch {
		case errors.Is(context.Cause(ctx), errCancelled):
			msg, status, action = "cancelled by the user", store.DeployCancelled, "deployment.cancelled"
			logw.printf("Deployment cancelled")
		case ctx.Err() != nil:
			// Shutting down: back in the queue, Recover starts it again on the next start.
			logw.printf("The panel is shutting down; this deployment starts again when it is back")
			if err := st.RequeueDeployment(final, dep.ID); err != nil {
				s.opts.Log.Error("requeue deployment", "error", err)
			}
			s.opts.Log.Info("deployment interrupted by shutdown, requeued", "app", app.Name, "deployment", dep.ID)
			return
		default:
			logw.printf("Deployment failed: %s", msg)
		}
		if err := st.FinishDeployment(final, dep.ID, status, msg); err != nil {
			s.opts.Log.Error("finish deployment", "error", err)
		}
		s.event(final, action, app, msg)
		s.opts.Log.Warn("deployment "+status, "app", app.Name, "deployment", dep.ID, "error", msg)
	}

	// phase records the step the deployment enters. The timeline is bookkeeping: a failed
	// write is logged, not fatal.
	phase := func(p string) {
		if err := st.EnterDeploymentPhase(final, dep.ID, p); err != nil {
			s.opts.Log.Error("record deployment phase", "deployment", dep.ID, "phase", p, "error", err)
		}
	}

	buildDone := func() {}
	// A build waits, still queued, for a free build slot; the slot is held until the image
	// exists. Pulls need none.
	rollback := dep.RollbackOf != ""
	if app.SourceType == store.SourceGit && !rollback {
		select {
		case s.builds <- struct{}{}:
		default:
			logw.printf("Waiting for a running build to finish (%d at a time)", cap(s.builds))
			select {
			case s.builds <- struct{}{}:
			case <-ctx.Done():
				fail("", ctx.Err())
				return
			}
		}
		held := true
		releaseSlot := func() {
			if held {
				held = false
				<-s.builds
			}
		}
		defer releaseSlot()
		buildDone = releaseSlot
	}

	if err := st.MarkDeploymentRunning(final, dep.ID); err != nil {
		fail("", err)
		return
	}
	// 1. Pull the image, or build it from Git. A rollback reuses the earlier image: a Git
	// app's is still on the server, an image app's is pulled again.
	image := app.Image
	if rollback {
		image = dep.Image
		version := image
		if dep.CommitSHA != "" {
			version = fmt.Sprintf("commit %s, image %s", dep.CommitSHA, image)
		}
		logw.printf("Rolling back %s to deployment %s (%s), without a build", app.Name, dep.RollbackOf, version)
	}
	switch {
	case rollback && app.SourceType == store.SourceGit:
		// Nothing to fetch: the image is still on the server.
	case app.SourceType == store.SourceGit:
		phase(store.PhaseBuilding)
		logw.printf("Deploying %s from %s (branch %s)", app.Name, app.Git.Repository, app.Git.Branch)
		var err error
		image, err = s.build(ctx, final, app, dep, logw)
		buildDone()
		if err != nil {
			fail("", err)
			return
		}
	default:
		phase(store.PhasePulling)
		if !rollback {
			logw.printf("Deploying %s (image %s)", app.Name, app.Image)
		}
		auth, err := s.registryAuth(ctx, app, image)
		if err != nil {
			fail("", err)
			return
		}
		if err := s.retry(ctx, logw, "Pull", func() error { return s.pull(ctx, image, auth, logw) }); err != nil {
			fail("", err)
			return
		}
	}

	// 2. Create and start the new container next to the old one.
	phase(store.PhaseStarting)
	mounts, err := s.prepareMounts(app, dep)
	if err != nil {
		fail("", err)
		return
	}
	name := containerName(app.ID, dep.ID)
	logw.printf("Creating container %s", name)
	entrypoint, command := startCommand(app)
	if command != nil {
		logw.printf("Start command: %s", app.StartCommand)
	}
	containerID, err := ex.CreateContainer(ctx, executor.ContainerSpec{
		Name:       name,
		Image:      image,
		Kind:       executor.KindApp,
		ResourceID: app.ID,
		Labels:     map[string]string{"deployment": dep.ID},
		CPUs:       app.CPULimit,
		MemoryMB:   app.MemoryMB,
		Env:        containerEnv(app, dep.Port),
		Entrypoint: entrypoint,
		Command:    command,
		Mounts:     mounts,
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
	check, saved, err := st.AppHealthCheck(final, app.ID)
	if err != nil {
		fail(containerID, err)
		return
	}
	if !saved {
		check = store.HealthCheck{Type: store.HealthRunning}
	}
	phase(store.PhaseHealthChecking)
	if err := s.waitHealthy(ctx, check, containerID, name, dep.Port, logw); err != nil {
		fail(containerID, err)
		return
	}

	// 3. Switch: the database first (it is the source of truth), then the proxy. From here
	// a cancel is refused.
	if !s.enterSwitch(dep.ID, ctx) {
		fail(containerID, ctx.Err())
		return
	}
	phase(store.PhaseSwitching)
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

	// 4. Remove every other container of this app. From here the deploy has succeeded.
	phase(store.PhaseDraining)
	s.removeOthers(final, app, containerID, logw)

	s.pruneImages(final, app.ID, image, logw)

	// An image app now runs the rolled-back image: its settings say so, so a redeploy does
	// not quietly return to the image it left.
	if rollback && app.SourceType == store.SourceImage && app.Image != image {
		if err := st.SetAppImage(final, app.ID, image); err != nil {
			logw.printf("Warning: could not set the app's image to %s: %v", image, err)
		} else {
			logw.printf("The app's image is now %s", image)
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

// retry runs step, and runs it again after each of RetryDelays while it fails for a
// temporary reason (see temporary). Other errors, and a cancel, return at once.
func (s *Service) retry(ctx context.Context, logw *logWriter, what string, step func() error) error {
	for i := 0; ; i++ {
		err := step()
		if err == nil || ctx.Err() != nil || i >= len(s.opts.RetryDelays) || !temporary(err) {
			return err
		}
		wait := s.opts.RetryDelays[i]
		logw.printf("%s failed for what looks like a temporary reason: %v", what, err)
		logw.printf("Trying again in %s (retry %d of %d)", wait, i+1, len(s.opts.RetryDelays))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// temporaryMarks are error texts of network trouble that often passes: DNS hiccups,
// dropped or refused connections, timeouts, a registry or Git host briefly overloaded.
// Missing images, bad credentials and unknown branches are not among them.
var temporaryMarks = []string{
	"temporary failure in name resolution", "could not resolve host", "no such host",
	"i/o timeout", "timeout awaiting", "tls handshake timeout", "connection timed out", "operation timed out",
	"connection reset", "connection refused", "broken pipe", "unexpected eof", "early eof",
	"the remote end hung up unexpectedly", "rpc failed",
	"502 bad gateway", "503 service unavailable", "504 gateway", "returned error: 502", "returned error: 503", "returned error: 504",
}

// temporary reports whether err looks like network trouble worth another try.
func temporary(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, m := range temporaryMarks {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// removeOthers removes every container of the app but keep. A leftover container is
// logged, not fatal: the next deploy removes it.
func (s *Service) removeOthers(ctx context.Context, app store.App, keep string, logw *logWriter) {
	ex := s.opts.Executor
	list, err := ex.ListContainers(ctx)
	if err != nil {
		logw.printf("Warning: could not list old containers: %v", err)
		return
	}
	for _, c := range list {
		if c.Kind != executor.KindApp || c.ResourceID != app.ID || c.ID == keep {
			continue
		}
		logw.printf("Removing old container %s", c.Name)
		if c.Running {
			if err := ex.StopContainer(ctx, c.ID, s.opts.StopGrace); err != nil {
				logw.printf("Warning: stop %s: %v", c.Name, err)
			}
		}
		if err := ex.RemoveContainer(ctx, c.ID, true); err != nil {
			logw.printf("Warning: remove %s: %v", c.Name, err)
		}
	}
}

// registryAuth returns the credentials an image app pulls with: none for a public image.
// They are only ever sent to the registry they belong to: an image on another host fails.
func (s *Service) registryAuth(ctx context.Context, app store.App, image string) (*executor.RegistryAuth, error) {
	if app.RegistryID == "" {
		return nil, nil
	}
	c, err := s.opts.Store.RegistryCredentials(ctx, app.RegistryID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errors.New("the app's registry was removed; choose another in its settings")
	}
	if err != nil {
		return nil, fmt.Errorf("registry credentials: %w", err)
	}
	if host := store.ImageServer(image); host != c.Server {
		return nil, fmt.Errorf("the image is on %s, but the app's registry is %s; credentials are only sent to their own registry", host, c.Server)
	}
	return &executor.RegistryAuth{Server: c.Server, Username: c.Username, Password: c.Password}, nil
}

// pull pulls an image, logging one line per layer per status change.
func (s *Service) pull(ctx context.Context, image string, auth *executor.RegistryAuth, logw *logWriter) error {
	if auth != nil {
		logw.printf("Pulling image %s (signed in to %s as %s)", image, auth.Server, auth.Username)
	} else {
		logw.printf("Pulling image %s", image)
	}
	last := map[string]string{}
	return s.opts.Executor.PullImage(ctx, image, auth, func(p executor.PullProgress) {
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
		err := s.retry(ctx, logw, "Git connection", func() (err error) {
			auth, err = s.opts.GitAuth.Credentials(ctx, src.ConnectionID, src.Repository)
			return err
		})
		if err != nil {
			return "", fmt.Errorf("git connection: %w", err)
		}
	}
	logw.printf("Fetching branch %s", src.Branch)
	var commit string
	err := s.retry(ctx, logw, "Fetch", func() (err error) {
		if err := os.RemoveAll(dir); err != nil { // a clone needs an empty place
			return err
		}
		commit, err = s.opts.Builder.Fetch(ctx, src, auth, dir, out)
		return err
	})
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

// containerEnv is the app's environment, plus PORT set to port (the deployment's) for a
// Git app that does not set it: images built by Railpack (and many others) listen on $PORT.
func containerEnv(app store.App, port int) map[string]string {
	if app.SourceType != store.SourceGit {
		return app.Environment
	}
	if _, ok := app.Environment["PORT"]; ok {
		return app.Environment
	}
	env := make(map[string]string, len(app.Environment)+1)
	for k, v := range app.Environment {
		env[k] = v
	}
	env["PORT"] = strconv.Itoa(port)
	return env
}

// startCommand replaces the image's entrypoint and command when the app sets a start
// command. A shell runs it, so $VARIABLES and $(...) expand as in a Procfile.
func startCommand(app store.App) (entrypoint, command []string) {
	if app.StartCommand == "" {
		return nil, nil
	}
	return []string{"/bin/sh", "-c"}, []string{app.StartCommand}
}

// KeepImages is how many built images of an app survive a deploy, the live one included.
// They are what a rollback can return to without a build.
const KeepImages = 5

// pruneImages removes an app's build images beyond the newest KeepImages tagged ones, and
// every dangling one (left when a commit is rebuilt under the same tag). The live image is
// never removed, and Docker refuses any image a container still uses. Failures are logged:
// the next deploy tries again.
func (s *Service) pruneImages(ctx context.Context, appID, live string, logw *logWriter) {
	images, err := s.opts.Executor.ListBuildImages(ctx, appID)
	if err != nil {
		logw.printf("Warning: could not list build images: %v", err)
		return
	}
	kept := 0
	for _, img := range images { // newest first
		if slices.Contains(img.Tags, live) {
			kept++
			continue
		}
		if len(img.Tags) > 0 && kept < KeepImages {
			kept++
			continue
		}
		name := img.ID
		if len(img.Tags) > 0 {
			name = img.Tags[0]
		}
		switch err := s.opts.Executor.RemoveImage(ctx, img.ID); {
		case err == nil:
			logw.printf("Removed old image %s", name)
		case errors.Is(err, executor.ErrInUse):
			// A stopped container of another deploy still holds it; it goes next time.
		default:
			logw.printf("Warning: remove image %s: %v", name, err)
		}
	}
}

// imageTag names the image built from one commit of an app: "app-<app id>:<commit>".
func imageTag(appID, commit string) string {
	return strings.ToLower("app-"+appID) + ":" + commit
}

// waitHealthy returns once the new container passes its health check. A check with no
// type set is the one of apps that never saved one: the container stays up for StartGrace.
// Whatever the check, a container that exits or restarts meanwhile fails at once.
func (s *Service) waitHealthy(ctx context.Context, h store.HealthCheck, containerID, name string, port int, logw *logWriter) error {
	second := func(n int) time.Duration { return time.Duration(n) * time.Second }
	start := time.Now()
	if h.Type == store.HealthRunning {
		stay := second(h.StartPeriod)
		if h.StartPeriod == 0 {
			stay = s.opts.StartGrace
		} else {
			logw.printf("Health check: the container must stay up for %s", stay)
		}
		for {
			if err := s.checkAlive(ctx, containerID, logw); err != nil {
				return err
			}
			left := stay - time.Since(start)
			if left <= 0 {
				return nil
			}
			if err := sleep(ctx, min(250*time.Millisecond, left)); err != nil {
				return err
			}
		}
	}

	if s.opts.OutsideNetwork {
		logw.printf("Health check: skipped, the panel runs outside Docker and cannot reach the container; it only has to stay up for %s", s.opts.StartGrace)
		return s.waitHealthy(ctx, store.HealthCheck{Type: store.HealthRunning}, containerID, name, port, logw)
	}
	target := net.JoinHostPort(name, strconv.Itoa(port))
	if h.Type == store.HealthHTTP {
		target = "http://" + target + h.Path
	}
	logw.printf("Health check: %s %s every %ds (timeout %ds); failures do not count during the first %ds, then %d in a row fail the deploy",
		strings.ToUpper(h.Type), target, h.Interval, h.Timeout, h.StartPeriod, h.Retries)
	failures, waiting := 0, false
	for attempt := 1; ; attempt++ {
		if err := s.checkAlive(ctx, containerID, logw); err != nil {
			return err
		}
		err := s.probe(ctx, h, name, port)
		if err == nil {
			logw.printf("Health check passed (attempt %d, after %s)", attempt, time.Since(start).Round(100*time.Millisecond))
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(start) < second(h.StartPeriod) {
			if !waiting {
				logw.printf("Not ready yet: %v", err)
				waiting = true
			}
		} else {
			failures++
			logw.printf("Health check failed (%d of %d): %v", failures, h.Retries, err)
			if failures >= h.Retries {
				return fmt.Errorf("health check failed %d times in a row: %v", failures, err)
			}
		}
		if err := sleep(ctx, second(h.Interval)); err != nil {
			return err
		}
	}
}

// probe runs one attempt of a TCP or HTTP health check against the container.
func (s *Service) probe(ctx context.Context, h store.HealthCheck, name string, port int) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.Timeout)*time.Second)
	defer cancel()
	addr := net.JoinHostPort(name, strconv.Itoa(port))
	if h.Type == store.HealthTCP {
		conn, err := s.opts.Dial(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return conn.Close()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+h.Path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "berth-health-check")
	client := &http.Client{
		Transport:     &http.Transport{DialContext: s.opts.Dial, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	ok := resp.StatusCode >= 200 && resp.StatusCode < 400
	if h.ExpectedStatus != 0 {
		ok = resp.StatusCode == h.ExpectedStatus
	}
	if !ok {
		if h.ExpectedStatus != 0 {
			return fmt.Errorf("answered %d, expected %d", resp.StatusCode, h.ExpectedStatus)
		}
		return fmt.Errorf("answered %d, expected 2xx or 3xx", resp.StatusCode)
	}
	return nil
}

// checkAlive fails when the container has crashed. Under the restart policy a crashing
// container is restarted at once, so "running" alone proves nothing: a restart, or the
// "restarting" state, counts as a crash too. Its last log lines go into the deploy log.
func (s *Service) checkAlive(ctx context.Context, containerID string, logw *logWriter) error {
	c, err := s.opts.Executor.InspectContainer(ctx, containerID)
	if err != nil {
		return err
	}
	if c.Running && c.State != "restarting" && c.Restarts == 0 {
		return nil
	}
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

// sleep waits d, or less when ctx ends first.
func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
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
	// Built images go too. A failure leaves disk used but the app gone, so it is logged only.
	if images, err := s.opts.Executor.ListBuildImages(ctx, appID); err != nil {
		s.opts.Log.Error("list build images", "app", appID, "error", err)
	} else {
		for _, img := range images {
			if err := s.opts.Executor.RemoveImage(ctx, img.ID); err != nil {
				s.opts.Log.Error("remove build image", "app", appID, "image", img.ID, "error", err)
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
