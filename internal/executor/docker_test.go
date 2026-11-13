package executor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

const testImage = "busybox:1.37"

// newTestDocker returns an executor on its own network and label namespace, so the test
// never touches containers of a real install. It skips when no Docker daemon answers.
func newTestDocker(t *testing.T) *Docker {
	t.Helper()
	if testing.Short() {
		t.Skip("needs Docker; skipped with -short")
	}
	name := fmt.Sprintf("paneltest-%d", time.Now().UnixNano())
	d, err := NewDocker(Options{Network: name, LabelPrefix: name})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Ping(ctx); err != nil {
		d.Close()
		t.Skipf("no Docker daemon: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		list, _ := d.ListContainers(ctx)
		for _, c := range list {
			_ = d.RemoveContainer(ctx, c.ID, true)
		}
		_, _ = d.cli.NetworkRemove(ctx, name, client.NetworkRemoveOptions{})
		d.Close()
	})
	return d
}

func TestDockerLifecycle(t *testing.T) {
	d := newTestDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Network: created once, second call is a no-op.
	for i := 0; i < 2; i++ {
		if err := d.EnsureNetwork(ctx); err != nil {
			t.Fatalf("EnsureNetwork #%d: %v", i+1, err)
		}
	}

	// Pull with progress.
	var updates int
	if err := d.PullImage(ctx, testImage, func(PullProgress) { updates++ }); err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	if updates == 0 {
		t.Error("PullImage reported no progress")
	}
	if err := d.PullImage(ctx, "busybox:no-such-tag-"+d.network, nil); err == nil {
		t.Error("PullImage of a missing tag succeeded")
	}

	// Create with identity labels, env, limits.
	name := d.network + "-app"
	id, err := d.CreateContainer(ctx, ContainerSpec{
		Name:       name,
		Image:      testImage,
		Kind:       KindApp,
		ResourceID: "app_01",
		Labels:     map[string]string{"deployment": "dep_01", d.labelManaged(): "false"},
		Env:        map[string]string{"GREETING": "hello"},
		Command:    []string{"sh", "-c", `echo "$GREETING out"; echo "err line" >&2; sleep 300`},
		CPUs:       0.5,
		MemoryMB:   64,
	})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	c, err := d.InspectContainer(ctx, id)
	if err != nil {
		t.Fatalf("InspectContainer: %v", err)
	}
	if c.Running || c.State != "created" {
		t.Errorf("after create: state=%q running=%v", c.State, c.Running)
	}
	if c.Name != name || c.Kind != KindApp || c.ResourceID != "app_01" || c.Labels["deployment"] != "dep_01" {
		t.Errorf("identity wrong: %+v", c)
	}
	if c.Labels[d.labelManaged()] != "true" {
		t.Error("a caller label overrode the managed label")
	}

	// Limits reached the daemon.
	raw, err := d.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if hc := raw.Container.HostConfig; hc.NanoCPUs != 500_000_000 || hc.Memory != 64*1024*1024 {
		t.Errorf("limits: cpus=%d memory=%d", hc.NanoCPUs, hc.Memory)
	}

	// Start.
	if err := d.StartContainer(ctx, id); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	c, err = d.InspectContainer(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Running || c.IP == "" || c.StartedAt.IsZero() {
		t.Errorf("after start: running=%v ip=%q started=%v", c.Running, c.IP, c.StartedAt)
	}

	// List sees it; a foreign namespace does not.
	list, err := d.ListContainers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id || !list[0].Running || list[0].ResourceID != "app_01" {
		t.Errorf("ListContainers = %+v", list)
	}
	other, err := NewDocker(Options{Network: d.network, LabelPrefix: d.prefix + "-other"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if list, err := other.ListContainers(ctx); err != nil || len(list) != 0 {
		t.Errorf("foreign namespace sees %d containers (err %v)", len(list), err)
	}

	// Reconcile: adopted when known, orphan when not.
	r, err := Reconcile(ctx, d, func(k Kind, rid string) bool { return k == KindApp && rid == "app_01" })
	if err != nil || len(r.Adopted) != 1 || len(r.Orphans) != 0 {
		t.Errorf("Reconcile known = %+v (err %v)", r, err)
	}
	r, err = Reconcile(ctx, d, func(Kind, string) bool { return false })
	if err != nil || len(r.Adopted) != 0 || len(r.Orphans) != 1 {
		t.Errorf("Reconcile unknown = %+v (err %v)", r, err)
	}

	// Logs: one-shot, both streams demultiplexed.
	lines := waitForLines(t, d, id, 2)
	var sawOut, sawErr bool
	for _, l := range lines {
		sawOut = sawOut || (l.Stream == "stdout" && l.Text == "hello out" && !l.Time.IsZero())
		sawErr = sawErr || (l.Stream == "stderr" && l.Text == "err line")
	}
	if !sawOut || !sawErr {
		t.Errorf("logs = %+v", lines)
	}

	// Logs: tail.
	var tail []LogLine
	if err := d.Logs(ctx, id, LogOptions{Tail: 1}, func(l LogLine) { tail = append(tail, l) }); err != nil {
		t.Fatalf("Logs tail: %v", err)
	}
	if len(tail) != 1 {
		t.Errorf("tail=1 returned %d lines", len(tail))
	}

	// Logs: since the future returns nothing.
	var since []LogLine
	if err := d.Logs(ctx, id, LogOptions{Since: time.Now().Add(time.Hour)}, func(l LogLine) { since = append(since, l) }); err != nil {
		t.Fatalf("Logs since: %v", err)
	}
	if len(since) != 0 {
		t.Errorf("since=+1h returned %d lines", len(since))
	}

	// Logs: follow blocks until cancelled.
	followCtx, stopFollow := context.WithCancel(ctx)
	var mu sync.Mutex
	var followed int
	done := make(chan error, 1)
	go func() {
		done <- d.Logs(followCtx, id, LogOptions{Follow: true}, func(LogLine) {
			mu.Lock()
			followed++
			mu.Unlock()
		})
	}()
	select {
	case err := <-done:
		t.Fatalf("follow returned early: %v", err)
	case <-time.After(1500 * time.Millisecond):
	}
	stopFollow()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("follow after cancel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("follow did not stop after cancel")
	}
	mu.Lock()
	if followed < 2 {
		t.Errorf("follow saw %d lines", followed)
	}
	mu.Unlock()

	// Stop, then remove; removing twice is fine.
	if err := d.StopContainer(ctx, id, time.Second); err != nil {
		t.Fatalf("StopContainer: %v", err)
	}
	c, err = d.InspectContainer(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Running || c.State != "exited" {
		t.Errorf("after stop: state=%q running=%v", c.State, c.Running)
	}
	for i := 0; i < 2; i++ {
		if err := d.RemoveContainer(ctx, id, false); err != nil {
			t.Fatalf("RemoveContainer #%d: %v", i+1, err)
		}
	}

	// Gone: typed not-found everywhere.
	if _, err := d.InspectContainer(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Inspect after remove: %v", err)
	}
	if err := d.StartContainer(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Start after remove: %v", err)
	}
	if err := d.Logs(ctx, id, LogOptions{}, func(LogLine) {}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Logs after remove: %v", err)
	}
}

func TestDockerPortsAndMounts(t *testing.T) {
	d := newTestDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := d.EnsureNetwork(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.PullImage(ctx, testImage, nil); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	id, err := d.CreateContainer(ctx, ContainerSpec{
		Name:       d.network + "-svc",
		Image:      testImage,
		Kind:       KindService,
		ResourceID: "svc_01",
		Command:    []string{"sleep", "300"},
		Ports:      []PortBinding{{HostIP: "127.0.0.1", HostPort: 0, ContainerPort: 8080}},
		Mounts:     []Mount{{Source: dir, Target: "/data", ReadOnly: true}},
		Restart:    RestartNo,
	})
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	raw, err := d.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	hc := raw.Container.HostConfig
	if len(hc.PortBindings) != 1 {
		t.Errorf("port bindings = %+v", hc.PortBindings)
	}
	for port, b := range hc.PortBindings {
		if port.String() != "8080/tcp" || len(b) != 1 || b[0].HostIP.String() != "127.0.0.1" {
			t.Errorf("binding %s = %+v", port, b)
		}
	}
	if len(hc.Binds) != 1 || !strings.HasSuffix(hc.Binds[0], ":/data:ro") {
		t.Errorf("binds = %v", hc.Binds)
	}
	if hc.RestartPolicy.Name != "no" {
		t.Errorf("restart policy = %q", hc.RestartPolicy.Name)
	}
	if hc.LogConfig.Config["max-size"] == "" {
		t.Error("log rotation not set")
	}
}

func TestCreateContainerValidation(t *testing.T) {
	d, err := NewDocker(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.CreateContainer(context.Background(), ContainerSpec{Name: "x", Image: "busybox"}); err == nil {
		t.Error("a spec without kind and resource id was accepted")
	}
}

// waitForLines polls one-shot logs until the container has written n lines.
func waitForLines(t *testing.T, d *Docker, id string, n int) []LogLine {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var lines []LogLine
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := d.Logs(ctx, id, LogOptions{}, func(l LogLine) { lines = append(lines, l) })
		cancel()
		if err != nil {
			t.Fatalf("Logs: %v", err)
		}
		if len(lines) >= n || time.Now().After(deadline) {
			return lines
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// A folder of a named volume can be mounted on its own, read-only or not; this is how the
// proxy gets its folders when the panel's data lives in a volume.
func TestDockerVolumeSubPath(t *testing.T) {
	d := newTestDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := d.EnsureNetwork(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.PullImage(ctx, testImage, nil); err != nil {
		t.Fatal(err)
	}
	vol := d.network + "-data"
	t.Cleanup(func() { _, _ = d.cli.VolumeRemove(context.Background(), vol, client.VolumeRemoveOptions{Force: true}) })

	run := func(name string, mounts []Mount, script string) []string {
		t.Helper()
		return runToEnd(ctx, t, d, ContainerSpec{
			Name: d.network + "-" + name, Image: testImage, Kind: KindService, ResourceID: name,
			Command: []string{"sh", "-c", script}, Mounts: mounts, Restart: RestartNo,
		})
	}

	// The whole volume, as the panel sees it: create the folders and a file.
	run("seed", []Mount{{Volume: vol, Target: "/data"}}, "mkdir -p /data/proxy/etc && echo hello > /data/proxy/etc/config.json")

	// Only the sub-folder, read-only.
	out := run("reader", []Mount{{Volume: vol, SubPath: "proxy/etc", Target: "/etc/caddy", ReadOnly: true}},
		"cat /etc/caddy/config.json; ls /etc/caddy; touch /etc/caddy/x 2>/dev/null && echo writable || echo read-only")
	if strings.Join(out, "|") != "hello|config.json|read-only" {
		t.Errorf("sub-path mount saw %q", out)
	}

	for name, bad := range map[string]Mount{
		"both sources":     {Source: "/tmp", Volume: vol, Target: "/x"},
		"no source":        {Target: "/x"},
		"subpath on bind":  {Source: "/tmp", SubPath: "a", Target: "/x"},
		"absolute subpath": {Volume: vol, SubPath: "/proxy", Target: "/x"},
		"escaping subpath": {Volume: vol, SubPath: "proxy/../..", Target: "/x"},
		"relative target":  {Volume: vol, Target: "x"},
	} {
		if _, err := d.CreateContainer(ctx, ContainerSpec{Name: d.network + "-bad", Image: testImage, Kind: KindService, ResourceID: "bad", Mounts: []Mount{bad}}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestDockerBuildImages(t *testing.T) {
	d := newTestDocker(t)
	ctx := context.Background()
	if err := d.PullImage(ctx, testImage, nil); err != nil {
		t.Fatal(err)
	}
	resource := "app1"
	build := func(tag, marker string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", "build", "-q", "-t", tag, "-")
		cmd.Stdin = strings.NewReader(fmt.Sprintf("FROM %s\nLABEL %s.build=%s marker=%s\n", testImage, d.prefix, resource, marker))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("docker build: %v\n%s", err, out)
		}
	}
	old, live := d.prefix+"-app:old", d.prefix+"-app:live"
	build(old, "1")
	build(live, "2")
	t.Cleanup(func() {
		imgs, _ := d.ListBuildImages(context.Background(), resource)
		for _, img := range imgs {
			_, _ = d.cli.ImageRemove(context.Background(), img.ID, client.ImageRemoveOptions{Force: true})
		}
	})

	imgs, err := d.ListBuildImages(ctx, resource)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 2 {
		t.Fatalf("build images = %+v, want 2", imgs)
	}
	byTag := map[string]string{}
	for _, img := range imgs {
		for _, tag := range img.Tags {
			byTag[tag] = img.ID
		}
	}
	if byTag[old] == "" || byTag[live] == "" {
		t.Fatalf("build images = %+v, want tags %s and %s", imgs, old, live)
	}
	if other, _ := d.ListBuildImages(ctx, "app2"); len(other) != 0 {
		t.Errorf("images of another resource = %+v", other)
	}

	id, err := d.CreateContainer(ctx, ContainerSpec{Name: d.prefix + "-img", Image: live, Kind: KindApp, ResourceID: resource, Command: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveImage(ctx, byTag[live]); !errors.Is(err, ErrInUse) {
		t.Errorf("remove image in use: %v, want ErrInUse", err)
	}
	if err := d.RemoveImage(ctx, byTag[old]); err != nil {
		t.Errorf("remove old image: %v", err)
	}
	if err := d.RemoveImage(ctx, byTag[old]); err != nil {
		t.Errorf("remove an image already gone: %v", err)
	}
	if err := d.RemoveContainer(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if imgs, _ := d.ListBuildImages(ctx, resource); len(imgs) != 1 || imgs[0].ID != byTag[live] {
		t.Errorf("build images after removal = %+v", imgs)
	}
}

// runToEnd creates and starts a container, waits for it to exit and returns its output.
func runToEnd(ctx context.Context, t *testing.T, d *Docker, spec ContainerSpec) []string {
	t.Helper()
	id, err := d.CreateContainer(ctx, spec)
	if err != nil {
		t.Fatalf("%s: create: %v", spec.Name, err)
	}
	if err := d.StartContainer(ctx, id); err != nil {
		t.Fatalf("%s: start: %v", spec.Name, err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		c, err := d.InspectContainer(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !c.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not finish", spec.Name)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var out []string
	_ = d.Logs(ctx, id, LogOptions{}, func(l LogLine) { out = append(out, l.Text) })
	return out
}

func TestDockerEntrypointOverride(t *testing.T) {
	d := newTestDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := d.EnsureNetwork(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.PullImage(ctx, testImage, nil); err != nil {
		t.Fatal(err)
	}
	// What an app's start command becomes: the shell expands variables and $(...).
	out := runToEnd(ctx, t, d, ContainerSpec{
		Name: d.network + "-start", Image: testImage, Kind: KindApp, ResourceID: "start", Restart: RestartNo,
		Env:        map[string]string{"PORT": "8080"},
		Entrypoint: []string{"/bin/sh", "-c"},
		Command:    []string{`echo "port=$PORT files=$(ls -d /etc | wc -l)"`},
	})
	if strings.Join(out, "|") != "port=8080 files=1" {
		t.Errorf("start command printed %q", out)
	}
}
