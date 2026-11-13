package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"panel/internal/executor"
)

const (
	// AdminSocket keeps the admin API on a unix socket in the data dir. Nothing on the
	// shared Docker network can reach it, so a compromised app cannot reconfigure the proxy.
	AdminSocket = "socket"
	// AdminTCP publishes the admin API on 127.0.0.1 of the host. Inside the shared network
	// the port is reachable by every container, so this is for development only.
	AdminTCP = "tcp"

	resourceID = "proxy"      // the proxy's resource id in its container labels
	specLabel  = "proxy.spec" // hash of the container spec; a change recreates the container
	adminPort  = 2019         // admin port inside the container in tcp mode
	maxUnixSun = 100          // unix socket paths are limited to ~104 bytes
)

// IsProxy reports whether a managed container is the proxy; startup reconciliation uses it.
func IsProxy(kind executor.Kind, id string) bool {
	return kind == executor.KindProxy && id == resourceID
}

// Options configures the Manager.
type Options struct {
	Executor executor.Executor
	Log      *slog.Logger
	Name     string // container name
	Image    string
	Dir      string // directory for Caddy's config, certificates and admin socket, as the panel sees it
	// Volume is set when Dir lives in a named Docker volume (the panel itself runs in a
	// container with its data in that volume). Caddy then mounts its folders from the
	// volume at VolumePath, instead of from host paths that do not exist.
	Volume        string
	VolumePath    string // Dir's location inside Volume, e.g. "proxy"
	HTTPPort      int    // host ports
	HTTPSPort     int
	Admin         string // AdminSocket or AdminTCP
	AdminPort     int    // AdminTCP: port on 127.0.0.1
	PanelUpstream string // host:port where Caddy reaches the panel
	// Source returns the routing state from the database. The Manager fills in
	// AdminListen and PanelUpstream itself.
	Source func(ctx context.Context) (Config, error)
}

// Manager runs the Caddy container and keeps its configuration equal to the database.
type Manager struct {
	opts  Options
	admin *Admin

	mu   sync.Mutex // serialises every change to the container and its configuration
	last Config     // the configuration Caddy is running
}

func New(opts Options) (*Manager, error) {
	if opts.Executor == nil || opts.Source == nil || opts.Log == nil {
		return nil, errors.New("proxy: executor, source and log are required")
	}
	dir, err := filepath.Abs(opts.Dir) // bind mounts need absolute host paths
	if err != nil {
		return nil, err
	}
	opts.Dir = dir
	if opts.Volume != "" && (opts.VolumePath == "" || path.IsAbs(opts.VolumePath) || strings.Contains(opts.VolumePath, "..")) {
		return nil, fmt.Errorf("proxy: invalid volume path %q", opts.VolumePath)
	}
	var addr string
	switch opts.Admin {
	case AdminSocket:
		sock := filepath.Join(dir, "run", "admin.sock")
		if len(sock) > maxUnixSun {
			return nil, fmt.Errorf("proxy: admin socket path is too long (%d bytes): use a shorter data dir or tcp admin mode", len(sock))
		}
		addr = "unix:" + sock
	case AdminTCP:
		addr = fmt.Sprintf("tcp:127.0.0.1:%d", opts.AdminPort)
	default:
		return nil, fmt.Errorf("proxy: unknown admin mode %q (socket, tcp)", opts.Admin)
	}
	admin, err := NewAdmin(addr)
	if err != nil {
		return nil, err
	}
	return &Manager{opts: opts, admin: admin}, nil
}

// adminListen is the admin address in Caddy's own notation, as seen inside the container.
func (m *Manager) adminListen() string {
	if m.opts.Admin == AdminSocket {
		return "unix//run/caddy/admin.sock"
	}
	return fmt.Sprintf(":%d", adminPort)
}

// Start makes sure the container runs with the current spec and configuration. It is safe
// to call at any time: a healthy proxy is left running and only gets the configuration
// pushed again.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, raw, err := m.build(ctx)
	if err != nil {
		return err
	}
	// Caddy boots from this file, so it must exist before the container starts and it must
	// always hold the latest configuration: a restart of Caddy alone must not lose routes.
	if err := m.writeBootConfig(raw); err != nil {
		return err
	}
	if err := m.ensureContainer(ctx); err != nil {
		return err
	}
	if err := m.waitReady(ctx); err != nil {
		return err
	}
	if err := m.admin.Load(ctx, raw); err != nil {
		return err
	}
	m.last = cfg
	return nil
}

// Apply rebuilds the configuration from the database and pushes it. Call it after any
// change to domains, routes or the panel domain.
func (m *Manager) Apply(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, raw, err := m.build(ctx)
	if err != nil {
		return err
	}
	if err := m.writeBootConfig(raw); err != nil {
		return err
	}
	if err := m.admin.Load(ctx, raw); err != nil {
		return err
	}
	m.last = cfg
	return nil
}

// SetUpstreams switches one route to new upstreams atomically, without reloading anything
// else. The caller must store the new upstreams in the database too, so the next Apply
// produces the same result.
func (m *Manager) SetUpstreams(ctx context.Context, routeID string, upstreams []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := -1
	for i, r := range m.last.Routes {
		if r.ID == routeID {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("proxy: no route %q in the running configuration", routeID)
	}
	// A route with both HTTPS and HTTP-only hosts is two Caddy routes; switch both.
	for _, key := range m.last.Routes[idx].keys() {
		if err := m.admin.setUpstreams(ctx, key, upstreams); err != nil {
			return err
		}
	}
	// Keep the boot file in step with what Caddy now runs.
	next := m.last
	next.Routes = append([]Route(nil), m.last.Routes...)
	next.Routes[idx].Upstreams = append([]string(nil), upstreams...)
	m.last = next
	raw, err := Build(next)
	if err != nil {
		return err
	}
	return m.writeBootConfig(raw)
}

// Supervise checks the proxy every interval and brings it back when the container is gone,
// stopped, or its admin API stops answering. It returns when ctx is cancelled.
func (m *Manager) Supervise(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	var lastErr string
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if m.healthy(ctx) {
			if lastErr != "" {
				m.opts.Log.Info("proxy recovered")
				lastErr = ""
			}
			continue
		}
		startCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := m.Start(startCtx)
		cancel()
		switch {
		case err == nil:
			m.opts.Log.Info("proxy restarted")
			lastErr = ""
		case ctx.Err() != nil:
			return
		case err.Error() != lastErr: // log a persistent failure once, not every tick
			m.opts.Log.Error("proxy down", "error", err)
			lastErr = err.Error()
		}
	}
}

func (m *Manager) healthy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, err := m.opts.Executor.InspectContainer(ctx, m.opts.Name)
	return err == nil && c.Running && m.admin.Ping(ctx) == nil
}

func (m *Manager) build(ctx context.Context) (Config, []byte, error) {
	cfg, err := m.opts.Source(ctx)
	if err != nil {
		return Config{}, nil, fmt.Errorf("proxy: read routes: %w", err)
	}
	cfg.AdminListen = m.adminListen()
	cfg.PanelUpstream = m.opts.PanelUpstream
	raw, err := Build(cfg)
	return cfg, raw, err
}

func (m *Manager) writeBootConfig(raw []byte) error {
	for _, d := range []string{"etc", "data", "config", "run"} {
		if err := os.MkdirAll(filepath.Join(m.opts.Dir, d), 0o700); err != nil {
			return fmt.Errorf("proxy dir: %w", err)
		}
	}
	// Write-then-rename inside the mounted directory: Caddy never sees a half-written file.
	path := filepath.Join(m.opts.Dir, "etc", "config.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("proxy config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("proxy config: %w", err)
	}
	return nil
}

func (m *Manager) spec() executor.ContainerSpec {
	o := m.opts
	spec := executor.ContainerSpec{
		Name:       o.Name,
		Image:      o.Image,
		Kind:       executor.KindProxy,
		ResourceID: resourceID,
		Command:    []string{"caddy", "run", "--config", "/etc/caddy/config.json"},
		Ports: []executor.PortBinding{
			{HostPort: o.HTTPPort, ContainerPort: 80},
			{HostPort: o.HTTPSPort, ContainerPort: 443},
			{HostPort: o.HTTPSPort, ContainerPort: 443, Protocol: "udp"}, // HTTP/3
		},
		Mounts: []executor.Mount{
			m.mount("etc", "/etc/caddy", true),
			m.mount("data", "/data", false),     // certificates and keys
			m.mount("config", "/config", false), // Caddy's own state
		},
		// Lets Caddy reach a panel that runs on the host (development) on Linux too.
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
	}
	if o.Admin == AdminSocket {
		spec.Mounts = append(spec.Mounts, m.mount("run", "/run/caddy", false))
	} else {
		spec.Ports = append(spec.Ports, executor.PortBinding{HostIP: "127.0.0.1", HostPort: o.AdminPort, ContainerPort: adminPort})
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%+v", spec)))
	spec.Labels = map[string]string{specLabel: hex.EncodeToString(sum[:8])}
	return spec
}

// mount maps one of the proxy's folders into the Caddy container.
func (m *Manager) mount(folder, target string, readOnly bool) executor.Mount {
	if m.opts.Volume != "" {
		return executor.Mount{Volume: m.opts.Volume, SubPath: path.Join(m.opts.VolumePath, folder), Target: target, ReadOnly: readOnly}
	}
	return executor.Mount{Source: filepath.Join(m.opts.Dir, folder), Target: target, ReadOnly: readOnly}
}

// ensureContainer leaves a running container with the wanted spec alone, starts a stopped
// one, and replaces one whose spec is out of date (new image, ports or admin mode).
func (m *Manager) ensureContainer(ctx context.Context) error {
	ex, spec := m.opts.Executor, m.spec()

	c, err := ex.InspectContainer(ctx, spec.Name)
	switch {
	case errors.Is(err, executor.ErrNotFound):
	case err != nil:
		return err
	case !IsProxy(c.Kind, c.ResourceID):
		return fmt.Errorf("proxy: a container named %s exists and is not managed by this panel; remove or rename it", spec.Name)
	case c.Labels[specLabel] == spec.Labels[specLabel]:
		if c.Running {
			return nil
		}
		return ex.StartContainer(ctx, c.ID)
	default:
		m.opts.Log.Info("proxy spec changed; recreating the container")
		if err := ex.RemoveContainer(ctx, c.ID, true); err != nil {
			return err
		}
	}

	if err := ex.EnsureNetwork(ctx); err != nil {
		return err
	}
	id, err := ex.CreateContainer(ctx, spec)
	if errors.Is(err, executor.ErrNotFound) { // image not present yet
		m.opts.Log.Info("pulling proxy image", "image", spec.Image)
		if err := ex.PullImage(ctx, spec.Image, nil, nil); err != nil {
			return err
		}
		id, err = ex.CreateContainer(ctx, spec)
	}
	if err != nil {
		return err
	}
	if err := ex.StartContainer(ctx, id); err != nil {
		// A container that cannot start (host port taken) must not linger half-created.
		_ = ex.RemoveContainer(context.WithoutCancel(ctx), id, true)
		return err
	}
	return nil
}

// waitReady waits for the admin API, and explains itself with Caddy's last log lines when
// it never comes up.
func (m *Manager) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := m.admin.Ping(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("proxy: admin API did not come up: %w%s", err, m.lastLogs(ctx))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (m *Manager) lastLogs(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var lines []string
	_ = m.opts.Executor.Logs(ctx, m.opts.Name, executor.LogOptions{Tail: 5}, func(l executor.LogLine) {
		lines = append(lines, l.Text)
	})
	if len(lines) == 0 {
		return ""
	}
	return "; caddy: " + strings.Join(lines, " | ")
}
