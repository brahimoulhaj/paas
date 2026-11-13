// Package executortest provides an in-memory Executor for tests that exercise the logic
// above the container host without needing Docker.
package executortest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"panel/internal/executor"
)

// Fake keeps containers in a map. The exported fields tune its behaviour; set them
// before use.
type Fake struct {
	// CrashOnStart lists images whose containers exit right after they start.
	CrashOnStart map[string]bool
	// RestartLoop lists images whose containers exit cleanly at once and are restarted by
	// the restart policy, so they look "running" most of the time (e.g. a bare shell image).
	RestartLoop map[string]bool
	// PullDelay makes every pull take this long (to test concurrency).
	PullDelay time.Duration
	// Private images need a login from Logins to pull; PullAuths records each pull's.
	Private   map[string]bool
	Logins    map[string]string
	PullAuths []*executor.RegistryAuth

	mu         sync.Mutex
	containers map[string]*executor.Container // by id
	logs       map[string][]executor.LogLine  // by container id
	seq        int
	images     map[string][]executor.Image // build images by resource id
	Pulled     []string                    // image refs, in order
}

var _ executor.Executor = (*Fake)(nil)

func New() *Fake {
	return &Fake{CrashOnStart: map[string]bool{}, RestartLoop: map[string]bool{}, containers: map[string]*executor.Container{}, images: map[string][]executor.Image{}, logs: map[string][]executor.LogLine{}}
}

func (f *Fake) Ping(context.Context) error          { return nil }
func (f *Fake) EnsureNetwork(context.Context) error { return nil }
func (f *Fake) Close() error                        { return nil }

// PullImage fails for any ref containing "missing".
// RegistryLogin accepts the credentials in Logins (server → "user:password").
func (f *Fake) RegistryLogin(_ context.Context, a executor.RegistryAuth) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Logins[a.Server] != a.Username+":"+a.Password {
		return executor.ErrUnauthorized
	}
	return nil
}

func (f *Fake) PullImage(ctx context.Context, ref string, auth *executor.RegistryAuth, onProgress func(executor.PullProgress)) error {
	f.mu.Lock()
	f.PullAuths = append(f.PullAuths, auth)
	private := f.Private[ref]
	f.mu.Unlock()
	if private && (auth == nil || f.RegistryLogin(ctx, *auth) != nil) {
		return fmt.Errorf("pull %s: unauthorized: authentication required", ref)
	}
	if f.PullDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(f.PullDelay):
		}
	}
	if strings.Contains(ref, "missing") {
		return fmt.Errorf("pull %s: manifest unknown", ref)
	}
	if onProgress != nil {
		onProgress(executor.PullProgress{Layer: "abc123", Status: "Pull complete"})
	}
	f.mu.Lock()
	f.Pulled = append(f.Pulled, ref)
	f.mu.Unlock()
	return nil
}

func (f *Fake) find(idOrName string) *executor.Container {
	if c, ok := f.containers[idOrName]; ok {
		return c
	}
	for _, c := range f.containers {
		if c.Name == idOrName {
			return c
		}
	}
	return nil
}

func (f *Fake) CreateContainer(_ context.Context, spec executor.ContainerSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.find(spec.Name) != nil {
		return "", fmt.Errorf("create container %s: name already in use", spec.Name)
	}
	f.seq++
	id := fmt.Sprintf("cid-%04d", f.seq)
	labels := map[string]string{}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	f.containers[id] = &executor.Container{
		ID: id, Name: spec.Name, Image: spec.Image, Kind: spec.Kind, ResourceID: spec.ResourceID,
		Labels: labels, State: "created", CreatedAt: time.Now(),
	}
	return id, nil
}

func (f *Fake) StartContainer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return fmt.Errorf("start container %s: %w", id, executor.ErrNotFound)
	}
	if f.CrashOnStart[c.Image] {
		c.State, c.Running, c.ExitCode = "exited", false, 1
		return nil
	}
	c.State, c.Running, c.StartedAt = "running", true, time.Now()
	if f.RestartLoop[c.Image] {
		c.Restarts = 1
	}
	return nil
}

func (f *Fake) StopContainer(_ context.Context, id string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return fmt.Errorf("stop container %s: %w", id, executor.ErrNotFound)
	}
	c.State, c.Running = "exited", false
	return nil
}

func (f *Fake) RemoveContainer(_ context.Context, id string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.find(id); c != nil {
		delete(f.containers, c.ID)
	}
	return nil
}

func (f *Fake) InspectContainer(_ context.Context, id string) (executor.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return executor.Container{}, fmt.Errorf("inspect container %s: %w", id, executor.ErrNotFound)
	}
	return *c, nil
}

func (f *Fake) ListContainers(context.Context) ([]executor.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]executor.Container, 0, len(f.containers))
	for _, c := range f.containers {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AddImage records a build image for resourceID, newer than every image added before.
// A tag already held by another image moves to the new one, leaving that one dangling,
// as a rebuild of the same tag does in Docker.
func (f *Fake) AddImage(resourceID, tag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, list := range f.images {
		for i := range list {
			for j, t := range list[i].Tags {
				if t == tag {
					list[i].Tags = append(list[i].Tags[:j:j], list[i].Tags[j+1:]...)
					break
				}
			}
		}
	}
	f.seq++
	img := executor.Image{ID: fmt.Sprintf("sha256:%04d", f.seq), Tags: []string{tag}, CreatedAt: time.Unix(int64(f.seq), 0)}
	f.images[resourceID] = append(f.images[resourceID], img)
}

func (f *Fake) ListBuildImages(_ context.Context, resourceID string) ([]executor.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]executor.Image, 0, len(f.images[resourceID]))
	for i := len(f.images[resourceID]) - 1; i >= 0; i-- {
		img := f.images[resourceID][i]
		img.Tags = append([]string(nil), img.Tags...)
		out = append(out, img)
	}
	return out, nil
}

// RemoveImage refuses an image whose tag a container uses, like Docker without force.
func (f *Fake) RemoveImage(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for res, list := range f.images {
		for i, img := range list {
			if img.ID != id {
				continue
			}
			for _, c := range f.containers {
				for _, t := range img.Tags {
					if c.Image == t {
						return fmt.Errorf("remove image %s: %w", id, executor.ErrInUse)
					}
				}
			}
			f.images[res] = append(list[:i:i], list[i+1:]...)
			return nil
		}
	}
	return nil
}

// AddLog appends a line to a container's output; a following Logs call receives it.
func (f *Fake) AddLog(idOrName, stream, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.find(idOrName); c != nil {
		f.logs[c.ID] = append(f.logs[c.ID], executor.LogLine{Stream: stream, Time: time.Now(), Text: text})
	}
}

// Logs emits the lines added with AddLog (plus one line for a crashed container). With
// Follow it keeps delivering new lines until the container stops or ctx is cancelled.
func (f *Fake) Logs(ctx context.Context, id string, opts executor.LogOptions, fn func(executor.LogLine)) error {
	f.mu.Lock()
	c := f.find(id)
	if c == nil {
		f.mu.Unlock()
		return fmt.Errorf("logs %s: %w", id, executor.ErrNotFound)
	}
	cid := c.ID
	if c.State == "exited" && c.ExitCode != 0 {
		f.mu.Unlock()
		fn(executor.LogLine{Stream: "stderr", Time: time.Now(), Text: "fatal: cannot start"})
		return nil
	}
	lines := append([]executor.LogLine(nil), f.logs[cid]...)
	f.mu.Unlock()

	sent := len(lines)
	if !opts.Since.IsZero() {
		kept := lines[:0]
		for _, l := range lines {
			if !l.Time.Before(opts.Since) {
				kept = append(kept, l)
			}
		}
		lines = kept
	} else if opts.Tail > 0 && len(lines) > opts.Tail {
		lines = lines[len(lines)-opts.Tail:]
	}
	for _, l := range lines {
		fn(l)
	}
	for opts.Follow {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
		f.mu.Lock()
		c := f.containers[cid]
		fresh := append([]executor.LogLine(nil), f.logs[cid][min(sent, len(f.logs[cid])):]...)
		running := c != nil && c.Running
		f.mu.Unlock()
		sent += len(fresh)
		for _, l := range fresh {
			fn(l)
		}
		if !running {
			return nil
		}
	}
	return nil
}
