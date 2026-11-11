// Package update keeps the panel itself up to date. It finds the container the panel runs
// in, asks the registry the image came from whether there is something newer, and swaps
// the container for one on the new image with a short-lived helper (see Swap).
//
// Two ways of publishing are understood:
//   - version tags (0.1.0 → 0.1.1): the newest higher semver tag in the repository wins;
//   - a moving tag (latest, stable): a new digest behind the same tag is an update.
//
// This package is Docker-specific on purpose: the panel always updates the daemon it runs on.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"panel/internal/brand"
)

// Status of the update machinery.
const (
	StatusIdle     = "idle"
	StatusChecking = "checking"
	StatusPulling  = "pulling"
	StatusApplying = "applying" // the helper is swapping containers; the panel is about to restart
)

// Reasons Apply refuses to start; anything else is a failed pull or helper.
var (
	ErrBusy      = errors.New("an update is already in progress")
	ErrNoUpdate  = errors.New("no update available; check first")
	ErrDeploying = errors.New("a deployment is running; update when it has finished")
)

// Info is what the UI shows.
type Info struct {
	Supported   bool      `json:"supported"`
	Reason      string    `json:"reason,omitempty"` // why updates are not supported here
	Version     string    `json:"version"`          // build version of the running panel
	Image       string    `json:"image,omitempty"`  // image reference the panel runs
	Mode        string    `json:"mode,omitempty"`   // "semver" or "digest"
	Available   bool      `json:"available"`
	Latest      string    `json:"latest,omitempty"`       // newer version tag, or short digest
	LatestImage string    `json:"latest_image,omitempty"` // reference that would be pulled
	CheckedAt   time.Time `json:"checked_at,omitzero"`
	Error       string    `json:"error,omitempty"`
	Status      string    `json:"status"`
	LastResult  *Result   `json:"last_result,omitempty"`
}

// Result is written by the helper when a swap ends, and read by the next panel start.
type Result struct {
	From       string    `json:"from"`
	To         string    `json:"to"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	FinishedAt time.Time `json:"finished_at"`
}

// Options configures the service.
type Options struct {
	Docker  *client.Client
	DataDir string
	Version string
	Log     *slog.Logger
	// Busy reports work that must not be cut off by a restart (a running deploy). Automatic
	// updates wait for it; a manual update is refused while it is true.
	Busy func(ctx context.Context) (bool, error)
	// Hostname of the panel's container; empty = os.Hostname (Docker sets it to the id).
	Hostname string
	Registry *Registry
	// OnResult is called once for each new result the helper writes (the helper finishes
	// after this panel has started, so results are noticed when status is next read).
	OnResult func(Result)
}

type Service struct {
	opts     Options
	mu       sync.Mutex
	info     Info
	lastSeen time.Time // FinishedAt of the newest result passed to OnResult
}

func New(opts Options) *Service {
	if opts.Registry == nil {
		opts.Registry = &Registry{}
	}
	s := &Service{opts: opts, info: Info{Version: opts.Version, Status: StatusIdle}}
	if s.opts.Registry.Insecure == nil {
		s.opts.Registry.Insecure = s.insecure
	}
	return s
}

// ResultPath is where the helper records how a swap ended.
func ResultPath(dataDir string) string { return filepath.Join(dataDir, "update-result.json") }

// LogPath is the helper's log.
func LogPath(dataDir string) string { return filepath.Join(dataDir, "update.log") }

func readResult(dataDir string) *Result {
	b, err := os.ReadFile(ResultPath(dataDir))
	if err != nil {
		return nil
	}
	var r Result
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return &r
}

// Info is the current state, with the helper's latest result read fresh from disk.
func (s *Service) Info() Info {
	r := readResult(s.opts.DataDir)
	s.mu.Lock()
	s.info.LastResult = r
	notify := r != nil && r.FinishedAt.After(s.lastSeen)
	if notify {
		s.lastSeen = r.FinishedAt
	}
	info := s.info
	s.mu.Unlock()
	if notify && s.opts.OnResult != nil {
		s.opts.OnResult(*r)
	}
	return info
}

func (s *Service) set(fn func(*Info)) {
	s.mu.Lock()
	fn(&s.info)
	s.mu.Unlock()
}

// self finds the panel's own container and the image it was created from.
func (s *Service) self(ctx context.Context) (container.InspectResponse, error) {
	host := s.opts.Hostname
	if host == "" {
		host, _ = os.Hostname()
	}
	if _, err := os.Stat("/.dockerenv"); err != nil && s.opts.Hostname == "" {
		return container.InspectResponse{}, errors.New("the panel is not running in a container")
	}
	res, err := s.opts.Docker.ContainerInspect(ctx, host, client.ContainerInspectOptions{})
	if err != nil {
		return container.InspectResponse{}, fmt.Errorf("cannot find the panel's own container: %w", err)
	}
	return res.Container, nil
}

// Check asks the registry whether a newer panel image exists.
func (s *Service) Check(ctx context.Context) Info {
	s.mu.Lock()
	if s.info.Status != StatusIdle {
		info := s.info
		s.mu.Unlock()
		return info
	}
	s.info.Status = StatusChecking
	s.mu.Unlock()

	next := s.check(ctx)
	s.set(func(i *Info) {
		*i = next
		i.Version, i.Status = s.opts.Version, StatusIdle
		i.CheckedAt = time.Now().UTC()
	})
	return s.Info()
}

// local works out what can be known without the registry: the panel's container, its
// image, whether that image came from a registry, and how updates are detected.
type local struct {
	info   Info
	ref    Ref
	digest []string // repo digests of the running image
}

func (s *Service) local(ctx context.Context) (local, bool) {
	self, err := s.self(ctx)
	if err != nil {
		return local{info: Info{Reason: err.Error()}}, false
	}
	ref := self.Config.Image
	img, err := s.opts.Docker.ImageInspect(ctx, self.Image)
	if err != nil {
		return local{info: Info{Image: ref, Reason: "cannot inspect the panel image: " + err.Error()}}, false
	}
	if len(img.RepoDigests) == 0 {
		return local{info: Info{Image: ref, Reason: "the panel image was built on this host, not pulled from a registry"}}, false
	}
	r, err := ParseRef(ref)
	if err != nil {
		return local{info: Info{Image: ref, Reason: err.Error()}}, false
	}
	mode := "digest"
	if _, ok := ParseVersion(r.Tag); ok {
		mode = "semver"
	}
	return local{info: Info{Supported: true, Image: ref, Mode: mode}, ref: r, digest: img.RepoDigests}, true
}

// Detect fills in the local facts at startup, so the UI can explain itself before the
// first registry check.
func (s *Service) Detect(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	l, _ := s.local(ctx)
	s.set(func(i *Info) {
		i.Supported, i.Reason, i.Image, i.Mode = l.info.Supported, l.info.Reason, l.info.Image, l.info.Mode
	})
}

func (s *Service) check(ctx context.Context) Info {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	l, ok := s.local(ctx)
	info := l.info
	if !ok {
		return info
	}
	if info.Mode == "semver" {
		cur, _ := ParseVersion(l.ref.Tag)
		tags, err := s.opts.Registry.Tags(ctx, l.ref)
		if err != nil {
			info.Error = err.Error()
			return info
		}
		if v, ok := Newest(cur, tags); ok {
			info.Available, info.Latest, info.LatestImage = true, v.Tag, l.ref.WithTag(v.Tag)
		}
		return info
	}
	dist, err := s.opts.Docker.DistributionInspect(ctx, info.Image, client.DistributionInspectOptions{})
	if err != nil {
		info.Error = "cannot reach the registry: " + err.Error()
		return info
	}
	remote := dist.Descriptor.Digest.String()
	if !hasDigest(l.digest, remote) {
		info.Available, info.Latest, info.LatestImage = true, shortDigest(remote), info.Image
	}
	return info
}

func hasDigest(repoDigests []string, digest string) bool {
	for _, d := range repoDigests {
		if strings.HasSuffix(d, "@"+digest) {
			return true
		}
	}
	return false
}

func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		d = d[:12]
	}
	return d
}

// insecure follows the daemon: registries listed as insecure, or inside an insecure CIDR
// (127.0.0.0/8 by default), are reached over HTTP.
func (s *Service) insecure(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	if h == "localhost" {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := s.opts.Docker.Info(ctx, client.InfoOptions{})
	if err != nil || info.Info.RegistryConfig == nil {
		return false
	}
	rc := info.Info.RegistryConfig
	if ic, ok := rc.IndexConfigs[host]; ok {
		return !ic.Secure
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		for _, p := range rc.InsecureRegistryCIDRs {
			if p.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// Apply pulls the newer image and starts the helper that swaps the panel's container. It
// returns once the helper runs; the panel is stopped by the helper a moment later.
func (s *Service) Apply(ctx context.Context) error {
	s.mu.Lock()
	if s.info.Status != StatusIdle {
		s.mu.Unlock()
		return ErrBusy
	}
	if !s.info.Available || s.info.LatestImage == "" {
		s.mu.Unlock()
		return ErrNoUpdate
	}
	target, from := s.info.LatestImage, s.info.Image
	s.info.Status, s.info.Error = StatusPulling, ""
	s.mu.Unlock()

	if s.opts.Busy != nil {
		if busy, err := s.opts.Busy(ctx); err != nil || busy {
			s.set(func(i *Info) { i.Status = StatusIdle })
			if err != nil {
				return err
			}
			return ErrDeploying
		}
	}

	err := s.startHelper(ctx, target, from)
	s.set(func(i *Info) {
		if err != nil {
			i.Status, i.Error = StatusIdle, err.Error()
		} else {
			i.Status = StatusApplying
		}
	})
	return err
}

func (s *Service) startHelper(ctx context.Context, target, from string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	self, err := s.self(ctx)
	if err != nil {
		return err
	}
	s.opts.Log.Info("pulling panel update", "image", target)
	pull, err := s.opts.Docker.ImagePull(ctx, target, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", target, err)
	}
	for msg, err := range pull.JSONMessages(ctx) {
		if err != nil {
			pull.Close()
			return fmt.Errorf("pull %s: %w", target, err)
		}
		if msg.Error != nil {
			pull.Close()
			return fmt.Errorf("pull %s: %s", target, msg.Error.Message)
		}
	}
	pull.Close()

	name := brand.Slug + "-updater"
	// The panel and `update` on the command line each have their own Service; a helper
	// already swapping containers must not be removed under it.
	if prev, err := s.opts.Docker.ContainerInspect(ctx, name, client.ContainerInspectOptions{}); err == nil &&
		prev.Container.State != nil && prev.Container.State.Running {
		return ErrBusy
	}
	_, _ = s.opts.Docker.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true})
	created, err := s.opts.Docker.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			// The helper runs from the image the panel runs now, which is known to work;
			// a broken new image must not take the rollback down with it.
			Image: self.Image,
			Cmd:   []string{"update-apply", "--container", self.ID, "--image", target, "--from", from},
			Env:   []string{brand.EnvPrefix() + "DATA_DIR=" + s.opts.DataDir},
			Labels: map[string]string{
				brand.Slug + ".managed":  "true",
				brand.Slug + ".kind":     "service",
				brand.Slug + ".resource": "updater",
			},
		},
		HostConfig: &container.HostConfig{
			Binds:       []string{"/var/run/docker.sock:/var/run/docker.sock", s.opts.DataDir + ":" + s.opts.DataDir},
			NetworkMode: container.NetworkMode(brand.Network()),
		},
		NetworkingConfig: &network.NetworkingConfig{},
	})
	if err != nil {
		return fmt.Errorf("create the update helper: %w", err)
	}
	if _, err := s.opts.Docker.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start the update helper: %w", err)
	}
	s.opts.Log.Info("update helper started; the panel restarts on the new image", "image", target)
	return nil
}

// Run checks shortly after start and then every interval, and applies an update on its own
// when auto() says so and nothing is busy.
func (s *Service) Run(ctx context.Context, interval time.Duration, auto func(context.Context) bool) {
	// The helper writes its result shortly after this panel is up; notice it without
	// waiting for someone to open the page.
	go func() {
		for range 12 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
			s.Info()
		}
	}()
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		info := s.Check(ctx)
		if info.Error != "" {
			s.opts.Log.Warn("update check failed", "error", info.Error)
		}
		if info.Available && auto(ctx) {
			if err := s.Apply(ctx); err != nil {
				s.opts.Log.Info("automatic update postponed", "reason", err)
			}
		}
		t.Reset(interval)
	}
}
