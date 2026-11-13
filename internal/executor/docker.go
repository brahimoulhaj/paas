package executor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"

	"panel/internal/brand"
)

// Options configures the Docker executor. The zero value is what the panel uses:
// the daemon from DOCKER_HOST (or the default socket) and names derived from the brand.
type Options struct {
	Host        string // Docker endpoint, e.g. "unix:///var/run/docker.sock"; empty = environment
	Network     string // shared network name; empty = brand.Network()
	LabelPrefix string // label namespace; empty = brand.Slug
}

// Docker is the Executor for the local Docker daemon.
type Docker struct {
	cli     *client.Client
	network string
	prefix  string
}

var _ Executor = (*Docker)(nil)

// NewDocker builds the client. It does not connect; call Ping to check the daemon.
func NewDocker(opts Options) (*Docker, error) {
	clientOpts := []client.Opt{client.FromEnv}
	if opts.Host != "" {
		clientOpts = append(clientOpts, client.WithHost(opts.Host))
	}
	cli, err := client.New(clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	d := &Docker{cli: cli, network: opts.Network, prefix: opts.LabelPrefix}
	if d.network == "" {
		d.network = brand.Network()
	}
	if d.prefix == "" {
		d.prefix = brand.Slug
	}
	return d, nil
}

// Label keys. Every managed container carries all three.
func (d *Docker) labelManaged() string  { return d.prefix + ".managed" }
func (d *Docker) labelKind() string     { return d.prefix + ".kind" }
func (d *Docker) labelResource() string { return d.prefix + ".resource" }

// Network is the name of the shared network.
func (d *Docker) Network() string { return d.network }

func (d *Docker) Close() error { return d.cli.Close() }

func (d *Docker) Ping(ctx context.Context) error {
	if _, err := d.cli.Ping(ctx, client.PingOptions{}); err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	return nil
}

func (d *Docker) EnsureNetwork(ctx context.Context) error {
	_, err := d.cli.NetworkInspect(ctx, d.network, client.NetworkInspectOptions{})
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("inspect network %s: %w", d.network, err)
	}
	_, err = d.cli.NetworkCreate(ctx, d.network, client.NetworkCreateOptions{
		Driver: "bridge",
		Labels: map[string]string{d.labelManaged(): "true"},
	})
	// Two starts racing each other both see "not found"; the loser's conflict is fine.
	if err != nil && !cerrdefs.IsConflict(err) && !cerrdefs.IsAlreadyExists(err) {
		return fmt.Errorf("create network %s: %w", d.network, err)
	}
	return nil
}

// RemoveNetwork deletes the shared network. Nothing in the panel does this in normal
// operation; it exists for uninstall and for tests that create a throwaway network.
func (d *Docker) RemoveNetwork(ctx context.Context) error {
	_, err := d.cli.NetworkRemove(ctx, d.network, client.NetworkRemoveOptions{})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove network %s: %w", d.network, err)
	}
	return nil
}

// dockerHubAddress is how the Docker daemon names Docker Hub in a login.
const dockerHubAddress = "https://index.docker.io/v1/"

func serverAddress(server string) string {
	if server == "" || server == "docker.io" {
		return dockerHubAddress
	}
	return server
}

// encodeAuth is the X-Registry-Auth header value: base64url of the JSON credentials.
func encodeAuth(a RegistryAuth) (string, error) {
	raw, err := json.Marshal(registry.AuthConfig{Username: a.Username, Password: a.Password, ServerAddress: serverAddress(a.Server)})
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(raw), nil
}

func (d *Docker) RegistryLogin(ctx context.Context, a RegistryAuth) error {
	_, err := d.cli.RegistryLogin(ctx, client.RegistryLoginOptions{
		Username: a.Username, Password: a.Password, ServerAddress: serverAddress(a.Server),
	})
	switch {
	case err == nil:
		return nil
	case cerrdefs.IsUnauthorized(err) || cerrdefs.IsPermissionDenied(err) || strings.Contains(strings.ToLower(err.Error()), "unauthorized"):
		return ErrUnauthorized
	}
	return fmt.Errorf("log in to %s: %w", a.Server, err)
}

func (d *Docker) PullImage(ctx context.Context, ref string, auth *RegistryAuth, onProgress func(PullProgress)) error {
	opts := client.ImagePullOptions{}
	if auth != nil {
		header, err := encodeAuth(*auth)
		if err != nil {
			return err
		}
		opts.RegistryAuth = header
	}
	resp, err := d.cli.ImagePull(ctx, ref, opts)
	if err != nil {
		return fmt.Errorf("pull %s: %w", ref, wrapNotFound(err))
	}
	defer resp.Close()
	// The stream must be read to the end: the pull is only finished then, and a failure
	// (unknown tag, registry error) arrives as a message inside it, not as an HTTP error.
	for msg, err := range resp.JSONMessages(ctx) {
		if err != nil {
			return fmt.Errorf("pull %s: %w", ref, err)
		}
		if msg.Error != nil {
			return fmt.Errorf("pull %s: %s", ref, msg.Error.Message)
		}
		if onProgress != nil {
			p := PullProgress{Layer: msg.ID, Status: msg.Status}
			if msg.Progress != nil {
				p.Current, p.Total = msg.Progress.Current, msg.Progress.Total
			}
			onProgress(p)
		}
	}
	return nil
}

func (d *Docker) CreateContainer(ctx context.Context, spec ContainerSpec) (string, error) {
	if spec.Name == "" || spec.Image == "" || spec.Kind == "" || spec.ResourceID == "" {
		return "", errors.New("create container: name, image, kind and resource id are required")
	}

	labels := map[string]string{}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	// Set last so a caller-supplied label can never override the identity labels.
	labels[d.labelManaged()] = "true"
	labels[d.labelKind()] = string(spec.Kind)
	labels[d.labelResource()] = spec.ResourceID

	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)

	exposed := network.PortSet{}
	bindings := network.PortMap{}
	for _, p := range spec.Ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		port, err := network.ParsePort(strconv.Itoa(p.ContainerPort) + "/" + proto)
		if err != nil {
			return "", fmt.Errorf("create container: port %d/%s: %w", p.ContainerPort, proto, err)
		}
		b := network.PortBinding{HostPort: strconv.Itoa(p.HostPort)}
		if p.HostIP != "" {
			ip, err := netip.ParseAddr(p.HostIP)
			if err != nil {
				return "", fmt.Errorf("create container: host ip %q: %w", p.HostIP, err)
			}
			b.HostIP = ip
		}
		exposed[port] = struct{}{}
		bindings[port] = append(bindings[port], b)
	}

	binds := make([]string, 0, len(spec.Mounts))
	var volumes []mount.Mount
	for _, m := range spec.Mounts {
		if m.VolumeFile {
			if m.Volume == "" || m.SubPath == "" || filepath.IsAbs(m.SubPath) || strings.Contains(m.SubPath, "..") {
				return "", errors.New("invalid managed file mount")
			}
			v, err := d.cli.VolumeInspect(ctx, m.Volume, client.VolumeInspectOptions{})
			if err != nil {
				return "", fmt.Errorf("inspect file storage volume: %w", err)
			}
			if v.Volume.Mountpoint == "" {
				return "", errors.New("file storage volume has no host mountpoint")
			}
			m.Source = filepath.Join(v.Volume.Mountpoint, m.SubPath)
			m.Volume, m.SubPath = "", ""
		}
		if !strings.HasPrefix(m.Target, "/") || (m.Source == "") == (m.Volume == "") ||
			(m.SubPath != "" && (m.Volume == "" || strings.HasPrefix(m.SubPath, "/") || strings.Contains(m.SubPath, ".."))) {
			return "", fmt.Errorf("create container: invalid mount %+v", m)
		}
		if m.Volume != "" {
			// Long syntax: the short "name:/target" form cannot select a sub-folder.
			vm := mount.Mount{Type: mount.TypeVolume, Source: m.Volume, Target: m.Target, ReadOnly: m.ReadOnly}
			if m.SubPath != "" {
				vm.VolumeOptions = &mount.VolumeOptions{Subpath: m.SubPath}
			}
			volumes = append(volumes, vm)
			continue
		}
		b := m.Source + ":" + m.Target
		if m.ReadOnly {
			b += ":ro"
		}
		binds = append(binds, b)
	}

	restart := spec.Restart
	if restart == "" {
		restart = RestartUnlessStopped
	}

	res, err := d.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: spec.Name,
		Config: &container.Config{
			Image:        spec.Image,
			Env:          env,
			Entrypoint:   spec.Entrypoint,
			Cmd:          spec.Command,
			Labels:       labels,
			ExposedPorts: exposed,
		},
		HostConfig: &container.HostConfig{
			NetworkMode:   container.NetworkMode(d.network),
			PortBindings:  bindings,
			Binds:         binds,
			Mounts:        volumes,
			ExtraHosts:    spec.ExtraHosts,
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyMode(restart)},
			Resources: container.Resources{
				NanoCPUs: int64(spec.CPUs * 1e9),
				Memory:   spec.MemoryMB * 1024 * 1024,
			},
			// Without rotation the default json-file driver grows until the disk is full.
			LogConfig: container.LogConfig{
				Type:   "json-file",
				Config: map[string]string{"max-size": "10m", "max-file": "3"},
			},
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{d.network: {}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("create container %s: %w", spec.Name, wrapNotFound(err))
	}
	return res.ID, nil
}

func (d *Docker) StartContainer(ctx context.Context, id string) error {
	if _, err := d.cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start container %s: %w", short(id), wrapNotFound(err))
	}
	return nil
}

func (d *Docker) StopContainer(ctx context.Context, id string, grace time.Duration) error {
	secs := int(grace.Round(time.Second) / time.Second)
	if secs < 0 {
		secs = 0
	}
	if _, err := d.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &secs}); err != nil {
		return fmt.Errorf("stop container %s: %w", short(id), wrapNotFound(err))
	}
	return nil
}

func (d *Docker) RemoveContainer(ctx context.Context, id string, force bool) error {
	_, err := d.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: force})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove container %s: %w", short(id), err)
	}
	return nil
}

func (d *Docker) InspectContainer(ctx context.Context, id string) (Container, error) {
	res, err := d.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return Container{}, fmt.Errorf("inspect container %s: %w", short(id), wrapNotFound(err))
	}
	in := res.Container
	c := Container{
		ID:       in.ID,
		Restarts: in.RestartCount,
		Name:     strings.TrimPrefix(in.Name, "/"),
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, in.Created)
	if in.Config != nil {
		c.Image = in.Config.Image
		d.applyLabels(&c, in.Config.Labels)
	}
	if st := in.State; st != nil {
		c.State = string(st.Status)
		c.Running = st.Running
		c.ExitCode = st.ExitCode
		c.OOMKilled = st.OOMKilled
		c.StartedAt, _ = time.Parse(time.RFC3339Nano, st.StartedAt)
		if st.Health != nil {
			c.Health = string(st.Health.Status)
		}
	}
	if in.NetworkSettings != nil {
		c.IP = d.ip(in.NetworkSettings.Networks)
	}
	return c, nil
}

func (d *Docker) ListContainers(ctx context.Context) ([]Container, error) {
	res, err := d.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", d.labelManaged()+"=true"),
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	out := make([]Container, 0, len(res.Items))
	for _, s := range res.Items {
		c := Container{
			ID:        s.ID,
			Image:     s.Image,
			State:     string(s.State),
			Running:   s.State == container.StateRunning,
			CreatedAt: time.Unix(s.Created, 0),
		}
		if len(s.Names) > 0 {
			c.Name = strings.TrimPrefix(s.Names[0], "/")
		}
		d.applyLabels(&c, s.Labels)
		if s.Health != nil && s.Health.Status != container.NoHealthcheck {
			c.Health = string(s.Health.Status)
		}
		if s.NetworkSettings != nil {
			c.IP = d.ip(s.NetworkSettings.Networks)
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (d *Docker) ListBuildImages(ctx context.Context, resourceID string) ([]Image, error) {
	res, err := d.cli.ImageList(ctx, client.ImageListOptions{
		Filters: make(client.Filters).Add("label", d.prefix+".build="+resourceID),
	})
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	out := make([]Image, 0, len(res.Items))
	for _, s := range res.Items {
		img := Image{ID: s.ID, CreatedAt: time.Unix(s.Created, 0)}
		for _, t := range s.RepoTags {
			if t != "<none>:<none>" {
				img.Tags = append(img.Tags, t)
			}
		}
		out = append(out, img)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (d *Docker) RemoveImage(ctx context.Context, id string) error {
	// Not forced: Docker refuses an image a container still uses, which is what keeps the
	// live image safe. Children are pruned so dangling parent layers go with it.
	_, err := d.cli.ImageRemove(ctx, id, client.ImageRemoveOptions{PruneChildren: true})
	switch {
	case err == nil, cerrdefs.IsNotFound(err):
		return nil
	case cerrdefs.IsConflict(err):
		return fmt.Errorf("remove image %s: %w", id, ErrInUse)
	default:
		return fmt.Errorf("remove image %s: %w", id, err)
	}
}

func (d *Docker) Logs(ctx context.Context, id string, opts LogOptions, fn func(LogLine)) error {
	o := client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Follow:     opts.Follow,
		Tail:       "all",
	}
	if opts.Tail > 0 {
		o.Tail = strconv.Itoa(opts.Tail)
	}
	if !opts.Since.IsZero() {
		o.Since = strconv.FormatFloat(float64(opts.Since.UnixNano())/1e9, 'f', 9, 64)
	}
	rc, err := d.cli.ContainerLogs(ctx, id, o)
	if err != nil {
		return fmt.Errorf("logs %s: %w", short(id), wrapNotFound(err))
	}
	defer rc.Close()
	if err := demuxLogs(rc, fn); err != nil && ctx.Err() == nil {
		return fmt.Errorf("logs %s: %w", short(id), err)
	}
	return ctx.Err()
}

func (d *Docker) applyLabels(c *Container, labels map[string]string) {
	c.Labels = labels
	c.Kind = Kind(labels[d.labelKind()])
	c.ResourceID = labels[d.labelResource()]
}

func (d *Docker) ip(nets map[string]*network.EndpointSettings) string {
	if ep := nets[d.network]; ep != nil && ep.IPAddress.IsValid() {
		return ep.IPAddress.String()
	}
	return ""
}

// wrapNotFound makes Docker's not-found errors match ErrNotFound.
func wrapNotFound(err error) error {
	if cerrdefs.IsNotFound(err) {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return err
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
