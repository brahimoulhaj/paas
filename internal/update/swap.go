package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"panel/internal/brand"
)

// SwapOptions describes one swap, run by the helper container (`panel update-apply`).
type SwapOptions struct {
	Docker    *client.Client
	DataDir   string
	Container string // id of the panel container to replace
	Image     string // new image, already pulled
	From      string // image it runs now, for the record
	Out       io.Writer
	// HealthTimeout bounds the wait for the new panel; zero = 90 s.
	HealthTimeout time.Duration
}

// Swap replaces the panel container with one created from the new image and the same
// configuration (mounts, env, networks, ports, restart policy, logging). The old container
// is renamed and stopped first, so the name and ports are free; if the new one does not
// answer its health check in time it is removed and the old one comes back.
func Swap(ctx context.Context, o SwapOptions) (err error) {
	logf := func(format string, args ...any) {
		fmt.Fprintf(o.Out, "%s %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
	}
	defer func() {
		res := Result{From: o.From, To: o.Image, OK: err == nil, FinishedAt: time.Now().UTC()}
		if err != nil {
			res.Error = err.Error()
			logf("update failed: %v", err)
		} else {
			logf("update finished: now running %s", o.Image)
		}
		if b, e := json.Marshal(res); e == nil {
			_ = os.WriteFile(ResultPath(o.DataDir), b, 0o600)
		}
	}()
	if o.HealthTimeout == 0 {
		o.HealthTimeout = 90 * time.Second
	}
	d := o.Docker

	insp, err := d.ContainerInspect(ctx, o.Container, client.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect the panel container: %w", err)
	}
	old := insp.Container
	name := strings.TrimPrefix(old.Name, "/")
	oldName := name + "-previous"
	logf("updating %s from %s to %s", name, old.Config.Image, o.Image)

	oldImg, err := d.ImageInspect(ctx, old.Image)
	if err != nil {
		return fmt.Errorf("inspect the current image: %w", err)
	}
	cfg := containerOwnConfig(*old.Config, oldImg.Config)
	cfg.Image = o.Image
	endpoints := map[string]*network.EndpointSettings{}
	for netName, ep := range old.NetworkSettings.Networks {
		endpoints[netName] = &network.EndpointSettings{Aliases: userAliases(ep.Aliases, old.ID)}
	}

	// Free the name, then the ports.
	_, _ = d.ContainerRemove(ctx, oldName, client.ContainerRemoveOptions{Force: true}) // leftover of an earlier attempt
	if _, err := d.ContainerRename(ctx, old.ID, client.ContainerRenameOptions{NewName: oldName}); err != nil {
		return fmt.Errorf("rename the old container: %w", err)
	}
	restoreOld := func(reason error) error {
		logf("rolling back: %v", reason)
		_, _ = d.ContainerRename(ctx, old.ID, client.ContainerRenameOptions{NewName: name})
		if _, e := d.ContainerStart(ctx, old.ID, client.ContainerStartOptions{}); e != nil {
			return fmt.Errorf("%w; restarting the old panel also failed: %v", reason, e)
		}
		return reason
	}
	grace := 20
	logf("stopping the old panel")
	if _, err := d.ContainerStop(ctx, old.ID, client.ContainerStopOptions{Timeout: &grace}); err != nil {
		return restoreOld(fmt.Errorf("stop the old container: %w", err))
	}

	created, err := d.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:             name,
		Config:           &cfg,
		HostConfig:       old.HostConfig,
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: endpoints},
	})
	if err != nil {
		return restoreOld(fmt.Errorf("create the new container: %w", err))
	}
	removeNew := func() {
		_, _ = d.ContainerRemove(context.WithoutCancel(ctx), created.ID, client.ContainerRemoveOptions{Force: true})
	}
	if _, err := d.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		removeNew()
		return restoreOld(fmt.Errorf("start the new container: %w", err))
	}
	logf("new panel started; waiting for its health check")
	if err := waitHealthy(ctx, healthURL(name, cfg.Env), o.HealthTimeout); err != nil {
		removeNew()
		return restoreOld(fmt.Errorf("new panel not healthy: %w", err))
	}

	_, _ = d.ContainerRemove(ctx, old.ID, client.ContainerRemoveOptions{Force: true})
	if err := rewriteComposeImage(filepath.Join(o.DataDir, "compose.yaml"), o.Image); err != nil {
		logf("note: compose.yaml not updated: %v", err)
	}
	return nil
}

// containerOwnConfig keeps what was set on the container itself (env file, compose labels,
// overrides) and drops what it inherited from its image, so the new image's own entrypoint,
// command, env defaults and labels apply. Docker merges image and container env again
// when the new container is created.
func containerOwnConfig(c container.Config, img *dockerspec.DockerOCIImageConfig) container.Config {
	c.Hostname = "" // Docker sets it to the new container's id
	if img == nil {
		return c
	}
	c.Env = without(c.Env, img.Env)
	if slices.Equal(c.Entrypoint, img.Entrypoint) {
		c.Entrypoint = nil
	}
	if slices.Equal(c.Cmd, img.Cmd) {
		c.Cmd = nil
	}
	if c.WorkingDir == img.WorkingDir {
		c.WorkingDir = ""
	}
	if c.User == img.User {
		c.User = ""
	}
	if c.StopSignal == img.StopSignal {
		c.StopSignal = ""
	}
	if c.Healthcheck != nil && img.Healthcheck != nil && reflect.DeepEqual(*c.Healthcheck, *img.Healthcheck) {
		c.Healthcheck = nil
	}
	labels := map[string]string{}
	for k, v := range c.Labels {
		if iv, ok := img.Labels[k]; !ok || iv != v {
			labels[k] = v
		}
	}
	c.Labels = labels
	return c
}

func without(list, drop []string) []string {
	out := []string{}
	for _, v := range list {
		if !slices.Contains(drop, v) {
			out = append(out, v)
		}
	}
	return out
}

// userAliases drops the aliases Docker adds itself (the short container id).
func userAliases(aliases []string, id string) []string {
	var out []string
	for _, a := range aliases {
		if len(a) >= 12 && strings.HasPrefix(id, a) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// healthURL is the new panel's health endpoint, reached over the shared network by name.
func healthURL(name string, env []string) string {
	port := "8080"
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, brand.EnvPrefix()+"ADDR="); ok {
			if _, p, err := net.SplitHostPort(v); err == nil && p != "" {
				port = p
			}
		}
	}
	return "http://" + net.JoinHostPort(name, port) + "/api/health"
}

func waitHealthy(ctx context.Context, url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := &http.Client{Timeout: 3 * time.Second}
	var last error
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		res, err := c.Do(req)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
			err = fmt.Errorf("health answered %s", res.Status)
		}
		last = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("no healthy answer within %s (last: %v)", timeout, last)
		case <-time.After(time.Second):
		}
	}
}

var composeImageLine = regexp.MustCompile(`(?m)^(\s+image:\s*)\S+(.*)$`)

// rewriteComposeImage points the installer's compose file at the new image, so a later
// `docker compose up` or installer run keeps it. A missing file is fine (not installed
// with the installer).
func rewriteComposeImage(path, image string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	loc := composeImageLine.FindSubmatchIndex(b)
	if loc == nil {
		return fmt.Errorf("no image line in %s", path)
	}
	out := append([]byte{}, b[:loc[3]]...)
	out = append(out, image...)
	out = append(out, b[loc[4]:]...)
	return os.WriteFile(path, out, 0o644)
}
