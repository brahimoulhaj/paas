package builder

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"panel/internal/store"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(root, "Dockerfile"), "FROM scratch\n")
	write(t, filepath.Join(root, "api", "Dockerfile"), "FROM scratch\n")
	write(t, filepath.Join(outside, "secret"), "key\n")
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "evil")))
	must(os.Symlink(outside, filepath.Join(root, "evildir")))
	must(os.Symlink("api/Dockerfile", filepath.Join(root, "link")))

	cases := []struct {
		rel     string
		dir     bool
		wantErr string
	}{
		{"", true, ""},
		{"api", true, ""},
		{"Dockerfile", false, ""},
		{"link", false, ""}, // a symlink that stays inside is fine
		{"missing", false, "not found"},
		{"evil", false, "outside the repository"},
		{"evildir", true, "outside the repository"},
		{"../" + filepath.Base(outside), true, "outside the repository"},
		{"api", false, "is not a file"},
		{"Dockerfile", true, "is not a folder"},
	}
	for _, c := range cases {
		_, err := inside(root, c.rel, c.dir)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("inside(%q) = %v", c.rel, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("inside(%q) = %v, want %q", c.rel, err, c.wantErr)
		}
	}
}

func TestRunReportsLastLine(t *testing.T) {
	var lines []string
	err := run(context.Background(), func(l string) { lines = append(lines, l) }, os.Environ(),
		"sh", "-c", "echo one; echo two >&2; echo 'fatal: it broke' >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "exit status 3: fatal: it broke") {
		t.Errorf("err = %v", err)
	}
	if strings.Join(lines, "|") != "one|two|fatal: it broke" {
		t.Errorf("lines = %q", lines)
	}
	if err := run(context.Background(), nil, os.Environ(), "true"); err != nil {
		t.Errorf("true: %v", err)
	}
}

func TestRunCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := run(ctx, nil, os.Environ(), "sleep", "10")
	if err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("err = %v after %s", err, time.Since(start))
	}
}

// TestBuildDockerfile builds a tiny image with the real docker CLI.
func TestBuildDockerfile(t *testing.T) {
	if testing.Short() {
		t.Skip("needs a Docker daemon")
	}
	if err := exec.Command("docker", "buildx", "version").Run(); err != nil {
		t.Skip("docker buildx not available")
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "svc", "build", "Dockerfile"), "FROM busybox:1.36\nCOPY hello.txt /hello.txt\n")
	write(t, filepath.Join(dir, "svc", "hello.txt"), "hi\n")
	tag := "panel-builder-test:" + strings.ToLower(t.Name())
	t.Cleanup(func() { exec.Command("docker", "image", "rm", "-f", tag).Run() })

	src := store.GitSource{Builder: store.BuilderDockerfile, BuildContext: "svc", Dockerfile: "build/Dockerfile"}
	var lines []string
	b := &Local{}
	if err := b.Build(context.Background(), src, dir, tag, "app1", func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatalf("build: %v\n%s", err, strings.Join(lines, "\n"))
	}
	out, err := exec.Command("docker", "image", "inspect", "-f", `{{index .Config.Labels "berth.build"}}`, tag).Output()
	if err != nil || strings.TrimSpace(string(out)) != "app1" {
		t.Errorf("label = %q, %v", out, err)
	}

	src.Dockerfile = "nope/Dockerfile"
	if err := b.Build(context.Background(), src, dir, tag, "app1", nil); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing Dockerfile: %v", err)
	}
	write(t, filepath.Join(dir, "svc", "bad", "Dockerfile"), "FROM busybox:1.36\nRUN exit 7\n")
	src.Dockerfile = "bad/Dockerfile"
	if err := b.Build(context.Background(), src, dir, tag, "app1", nil); err == nil || !strings.Contains(err.Error(), "docker build") {
		t.Errorf("failing build: %v", err)
	}
}

func TestGitEnvAuth(t *testing.T) {
	env, err := gitEnv("https://github.com/org/app.git", Auth{})
	if err != nil || strings.Contains(strings.Join(env, "\n"), "GIT_CONFIG_COUNT") {
		t.Errorf("public: %v, %v", env, err)
	}
	env, err = gitEnv("https://github.com/org/app.git", Auth{Username: "x-access-token", Password: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	// base64("x-access-token:tok"), scoped to the repository's host.
	for _, want := range []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic eC1hY2Nlc3MtdG9rZW46dG9r", "GIT_TERMINAL_PROMPT=0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("env lacks %q", want)
		}
	}
	if _, err := gitEnv("http://github.com/org/app.git", Auth{Username: "u", Password: "p"}); err == nil {
		t.Error("credentials were allowed over plain http")
	}
}

// TestRailpackPinMatchesImages keeps the Railpack CLI baked into the images at the version
// whose BuildKit frontend the builder asks for.
func TestRailpackPinMatchesImages(t *testing.T) {
	re := regexp.MustCompile(`(?m)^ARG RAILPACK_VERSION=(\S+)$`)
	for _, f := range []string{"../../Dockerfile", "../../dev/api.Dockerfile"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		m := re.FindSubmatch(raw)
		if m == nil || string(m[1]) != RailpackVersion {
			t.Errorf("%s pins Railpack %q, builder.RailpackVersion is %q", f, m, RailpackVersion)
		}
	}
}

// stubTools writes fake docker and railpack binaries that log their arguments.
func stubTools(t *testing.T) (*Local, func() []string) {
	t.Helper()
	bin := t.TempDir()
	logFile := filepath.Join(bin, "calls.log")
	for _, name := range []string{"docker", "railpack"} {
		write(t, filepath.Join(bin, name), "#!/bin/sh\necho \""+name+" $*\" >> "+logFile+"\n")
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	calls := func() []string {
		raw, _ := os.ReadFile(logFile)
		os.Remove(logFile)
		var out []string
		for _, c := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if !strings.HasPrefix(c, "docker buildx ls ") { // the capped-builder sweep, tested in TestBuildLimits
				out = append(out, c)
			}
		}
		return out
	}
	return &Local{Docker: filepath.Join(bin, "docker"), Railpack: filepath.Join(bin, "railpack")}, calls
}

func TestBuildAutoPicksBuilder(t *testing.T) {
	b, calls := stubTools(t)
	parent, err := filepath.EvalSymlinks(t.TempDir()) // the builder resolves symlinks (macOS: /var → /private/var)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "dep1")
	write(t, filepath.Join(dir, "api", "package.json"), "{}\n")
	src := store.GitSource{Builder: store.BuilderAuto, BuildContext: "api", Dockerfile: "Dockerfile"}
	var lines []string
	line := func(l string) { lines = append(lines, l) }

	// No Dockerfile: Railpack plans, builds through its frontend, then the label step re-tags.
	if err := b.Build(context.Background(), src, dir, "app-x:abc", "app1", line); err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 4 ||
		!strings.HasPrefix(got[0], "railpack prepare "+filepath.Join(dir, "api")) ||
		!strings.Contains(got[1], "BUILDKIT_SYNTAX="+RailpackFrontend) || !strings.Contains(got[1], "--tag app-x:abc-railpack") ||
		!strings.Contains(got[2], "--tag app-x:abc --label berth.build=app1") ||
		got[3] != "docker image rm app-x:abc-railpack" {
		t.Errorf("railpack calls:\n%s", strings.Join(got, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "No api/Dockerfile: building with Railpack "+RailpackVersion) {
		t.Errorf("lines = %q", lines)
	}
	if left, _ := filepath.Glob(filepath.Join(parent, "dep1-railpack-*")); len(left) != 0 {
		t.Errorf("plan folder left behind: %v", left)
	}

	// With a Dockerfile at the configured path: the Dockerfile wins.
	write(t, filepath.Join(dir, "api", "Dockerfile"), "FROM scratch\n")
	if err := b.Build(context.Background(), src, dir, "app-x:abc", "app1", line); err != nil {
		t.Fatal(err)
	}
	if got := calls(); len(got) != 1 || !strings.Contains(got[0], "--file "+filepath.Join(dir, "api", "Dockerfile")) {
		t.Errorf("dockerfile calls: %q", got)
	}

	// Railpack forced, Dockerfile ignored.
	src.Builder = store.BuilderRailpack
	if err := b.Build(context.Background(), src, dir, "app-x:abc", "app1", nil); err != nil {
		t.Fatal(err)
	}
	if got := calls(); len(got) != 4 || !strings.HasPrefix(got[0], "railpack prepare") {
		t.Errorf("forced railpack calls: %q", got)
	}
}

// TestBuildRailpack builds a tiny Node app with the real railpack CLI and frontend. It needs
// railpack on PATH (or BERTH_TEST_RAILPACK) and network access; the first run downloads a
// Node toolchain and takes minutes.
func TestBuildRailpack(t *testing.T) {
	if testing.Short() {
		t.Skip("needs a Docker daemon and network")
	}
	rp := os.Getenv("BERTH_TEST_RAILPACK")
	if rp == "" {
		var err error
		if rp, err = exec.LookPath("railpack"); err != nil {
			t.Skip("railpack not installed; set BERTH_TEST_RAILPACK")
		}
	}
	dir := filepath.Join(t.TempDir(), "dep")
	write(t, filepath.Join(dir, "package.json"), `{"name":"t","version":"1.0.0","scripts":{"start":"node index.js"}}`)
	write(t, filepath.Join(dir, "index.js"), `require("http").createServer((q,r)=>r.end("ok")).listen(process.env.PORT||3000)`+"\n")
	tag := "panel-builder-test:" + strings.ToLower(t.Name())
	t.Cleanup(func() { exec.Command("docker", "image", "rm", "-f", tag).Run() })

	var lines []string
	b := &Local{Railpack: rp}
	src := store.GitSource{Builder: store.BuilderAuto, Dockerfile: "Dockerfile"}
	if err := b.Build(context.Background(), src, dir, tag, "app1", func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatalf("build: %v\n%s", err, strings.Join(lines, "\n"))
	}
	out, err := exec.Command("docker", "image", "inspect", "-f", `{{index .Config.Labels "berth.build"}} {{json .Config.Cmd}}`, tag).Output()
	if err != nil || strings.TrimSpace(string(out)) != `app1 ["npm run start"]` {
		t.Errorf("image = %q, %v", out, err)
	}
	if err := exec.Command("docker", "image", "inspect", tag+"-railpack").Run(); err == nil {
		t.Error("the unlabelled image is still tagged")
	}

	// A folder Railpack cannot plan fails with its own message.
	empty := filepath.Join(t.TempDir(), "empty")
	write(t, filepath.Join(empty, "README"), "nothing to build\n")
	if err := b.Build(context.Background(), src, empty, tag, "app1", nil); err == nil || !strings.Contains(err.Error(), "railpack") {
		t.Errorf("unbuildable folder: %v", err)
	}
}

// TestBuildLimits checks that capped builds run on a builder of their own, created once
// with the limits, that builders left from other limits are removed, and that the label
// step stays on the daemon's own builder.
func TestBuildLimits(t *testing.T) {
	bin := t.TempDir()
	logFile := filepath.Join(bin, "calls.log")
	builders := filepath.Join(bin, "builders")
	// `docker buildx ls` prints the builders file; every call is logged.
	write(t, filepath.Join(bin, "docker"), "#!/bin/sh\necho \"docker $*\" >> "+logFile+"\n"+
		"if [ \"$1 $2\" = \"buildx ls\" ]; then cat "+builders+"; fi\n")
	write(t, filepath.Join(bin, "railpack"), "#!/bin/sh\necho \"railpack $*\" >> "+logFile+"\n")
	for _, f := range []string{"docker", "railpack"} {
		if err := os.Chmod(filepath.Join(bin, f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	calls := func() []string {
		raw, _ := os.ReadFile(logFile)
		os.Remove(logFile)
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
	b := &Local{Docker: filepath.Join(bin, "docker"), Railpack: filepath.Join(bin, "railpack"),
		Limits: Limits{CPUs: 1.5, MemoryMB: 2048}}
	name := b.builderName(b.Limits)
	write(t, builders, "default\norbstack\nberth-build-0123abcd\n")

	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "dep1")
	write(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\n")
	src := store.GitSource{Builder: store.BuilderDockerfile, Dockerfile: "Dockerfile"}
	var lines []string
	if err := b.Build(context.Background(), src, dir, "app-x:abc", "app1", func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	got := calls()
	want := []string{
		"docker buildx ls --format {{.Name}}",
		"docker buildx rm --force berth-build-0123abcd",
		"docker buildx create --name " + name + " --driver docker-container --driver-opt image=" + BuildKitImage +
			" --driver-opt cpu-period=100000 --driver-opt cpu-quota=150000 --driver-opt memory=2048m --driver-opt memory-swap=2048m --bootstrap",
	}
	if len(got) != 4 || strings.Join(got[:3], "\n") != strings.Join(want, "\n") ||
		!strings.HasPrefix(got[3], "docker buildx build --builder "+name+" --progress=plain") {
		t.Errorf("first build calls:\n%s", strings.Join(got, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "Creating the build container (1.5 CPU, 2048 MB memory)") {
		t.Errorf("lines = %q", lines)
	}

	// The builder is known now: the next build goes straight to it. Railpack's label step
	// runs on the daemon's builder, which holds the image just loaded.
	write(t, builders, "default\n"+name+"\n")
	src.Builder = store.BuilderRailpack
	if err := b.Build(context.Background(), src, dir, "app-x:abc", "app1", nil); err != nil {
		t.Fatal(err)
	}
	got = calls()
	if len(got) != 4 || !strings.HasPrefix(got[1], "docker buildx build --builder "+name+" ") ||
		strings.Contains(got[2], "--builder") || !strings.Contains(got[2], "--label berth.build=app1") {
		t.Errorf("railpack calls:\n%s", strings.Join(got, "\n"))
	}

	// New limits: a new builder, the old one removed.
	b.Limits = Limits{MemoryMB: 1024}
	write(t, builders, "default\n"+name+"\n")
	if err := b.Build(context.Background(), store.GitSource{Builder: store.BuilderDockerfile, Dockerfile: "Dockerfile"}, dir, "app-x:abc", "app1", nil); err != nil {
		t.Fatal(err)
	}
	got = calls()
	if len(got) != 4 || got[1] != "docker buildx rm --force "+name ||
		strings.Contains(got[2], "cpu-quota") || !strings.Contains(got[2], "memory=1024m") {
		t.Errorf("changed limits calls:\n%s", strings.Join(got, "\n"))
	}

	// Limits read per build (Settings): a build running on the current builder keeps it
	// when the limits change under it; the next build makes a new one and leaves it alone.
	name = b.builderName(b.Limits)
	_, release, err := b.ensureBuilder(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	calls()
	b.LimitsFunc = func(context.Context) (Limits, error) { return Limits{CPUs: 2}, nil }
	write(t, builders, "default\n"+name+"\n")
	_, release2, err := b.ensureBuilder(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got = calls()
	if len(got) != 2 || got[0] != "docker buildx ls --format {{.Name}}" || !strings.Contains(got[1], "cpu-quota=200000") {
		t.Errorf("a builder in use was touched:\n%s", strings.Join(got, "\n"))
	}
	release()
	release2()

	// No limits: stale capped builders go, then the daemon's own builder, nothing created.
	b.LimitsFunc = func(context.Context) (Limits, error) { return Limits{}, nil }
	write(t, builders, "default\n"+name+"\n"+b.builderName(Limits{CPUs: 2})+"\n")
	if err := b.Build(context.Background(), store.GitSource{Builder: store.BuilderDockerfile, Dockerfile: "Dockerfile"}, dir, "app-x:abc", "app1", nil); err != nil {
		t.Fatal(err)
	}
	got = calls()
	if len(got) != 4 || got[1] != "docker buildx rm --force "+name || got[2] != "docker buildx rm --force "+b.builderName(Limits{CPUs: 2}) ||
		strings.Contains(got[3], "--builder") {
		t.Errorf("uncapped calls:\n%s", strings.Join(got, "\n"))
	}
	if err := b.Build(context.Background(), store.GitSource{Builder: store.BuilderDockerfile, Dockerfile: "Dockerfile"}, dir, "app-x:abc", "app1", nil); err != nil {
		t.Fatal(err)
	}
	if got := calls(); len(got) != 1 {
		t.Errorf("second uncapped build looked for builders again: %q", got)
	}

	// A failing limits source fails the build before anything runs.
	b.LimitsFunc = func(context.Context) (Limits, error) { return Limits{}, errors.New("db closed") }
	if err := b.Build(context.Background(), store.GitSource{Builder: store.BuilderDockerfile, Dockerfile: "Dockerfile"}, dir, "app-x:abc", "app1", nil); err == nil || !strings.Contains(err.Error(), "read build limits") {
		t.Errorf("limits error: %v", err)
	}
}

// TestBuildMemoryCapEnforced runs a step that needs more memory than the cap on a real
// capped builder and expects it killed. It never pulls BuildKit: without the pinned image
// already local it skips (`docker pull` it once to run this).
func TestBuildMemoryCapEnforced(t *testing.T) {
	if testing.Short() {
		t.Skip("needs a Docker daemon")
	}
	if err := exec.Command("docker", "image", "inspect", BuildKitImage).Run(); err != nil {
		t.Skip(BuildKitImage + " not pulled")
	}
	b := &Local{Limits: Limits{MemoryMB: 256}}
	t.Cleanup(func() { exec.Command("docker", "buildx", "rm", "--force", b.builderName(b.Limits)).Run() })
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Dockerfile"), "FROM busybox:1.36\nRUN dd if=/dev/zero of=/dev/null bs=400M count=1\n")
	tag := "panel-builder-test:" + strings.ToLower(t.Name())
	t.Cleanup(func() { exec.Command("docker", "image", "rm", "-f", tag).Run() })
	src := store.GitSource{Builder: store.BuilderDockerfile, Dockerfile: "Dockerfile"}
	var lines []string
	err := b.Build(context.Background(), src, dir, tag, "app1", func(l string) { lines = append(lines, l) })
	if err == nil {
		t.Fatalf("a 400 MB step passed a 256 MB cap:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "cannot allocate memory") && !strings.Contains(strings.Join(lines, "\n"), "Killed") {
		t.Errorf("not killed by the cap: %v\n%s", err, strings.Join(lines, "\n"))
	}
}

// TestFetchOverSSH clones with a deploy key from a throwaway SSH Git server in Docker: the
// host key is trusted the first time and pinned, a changed one is refused, and the key
// file does not outlive the clone. Needs Docker and network (apk) the first time.
func TestFetchOverSSH(t *testing.T) {
	if testing.Short() {
		t.Skip("needs a Docker daemon")
	}
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skip("no Docker daemon")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	sshPub, _ := ssh.NewPublicKey(pub)
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))

	script := `set -e
apk add --no-cache openssh git >/dev/null
ssh-keygen -A >/dev/null
adduser -D -s /bin/sh git && sed -i 's/^git:!/git:*/' /etc/shadow
mkdir -p /home/git/.ssh && echo "$KEY" > /home/git/.ssh/authorized_keys
chown -R git /home/git && chmod 700 /home/git/.ssh && chmod 600 /home/git/.ssh/authorized_keys
su git -c 'git init -q --bare /home/git/app.git && git init -q /tmp/w && cd /tmp/w && git config user.email t@t && git config user.name t && echo hi > f && git add f && git commit -qm init && git push -q /home/git/app.git HEAD:main'
exec /usr/sbin/sshd -D -e`
	out, err := exec.Command("docker", "run", "-d", "--rm", "-p", "127.0.0.1::22", "-e", "KEY="+authorized, "alpine:3.22", "sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("start ssh server: %v", err)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", id).Run() })
	portOut, err := exec.Command("docker", "port", id, "22/tcp").Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	addr := strings.TrimSpace(strings.Split(string(portOut), "\n")[0])
	_, port, _ := strings.Cut(addr, ":")

	// Wait for sshd: it starts after apk has installed it.
	deadline := time.Now().Add(2 * time.Minute)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			banner := make([]byte, 4)
			n, _ := c.Read(banner)
			c.Close()
			if string(banner[:n]) == "SSH-" {
				break
			}
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", id).CombinedOutput()
			t.Fatalf("ssh server did not start:\n%s", logs)
		}
		time.Sleep(300 * time.Millisecond)
	}

	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(parent, "ssh", "known_hosts")
	b := &Local{KnownHosts: knownHosts}
	src := store.GitSource{Repository: "ssh://git@127.0.0.1:" + port + "/home/git/app.git", Branch: "main"}
	auth := Auth{SSHKey: string(pem.EncodeToMemory(block))}
	var lines []string
	sha, err := b.Fetch(context.Background(), src, auth, filepath.Join(parent, "builds", "d1"), func(l string) { lines = append(lines, l) })
	if err != nil || !shaRe.MatchString(sha) {
		t.Fatalf("fetch = %q, %v\n%s", sha, err, strings.Join(lines, "\n"))
	}
	if raw, err := os.ReadFile(filepath.Join(parent, "builds", "d1", "f")); err != nil || string(raw) != "hi\n" {
		t.Errorf("cloned file = %q, %v", raw, err)
	}
	if kh, _ := os.ReadFile(knownHosts); !strings.Contains(string(kh), "[127.0.0.1]:"+port) {
		t.Errorf("host key not pinned: %q", kh)
	}
	if left, _ := filepath.Glob(filepath.Join(parent, "builds", "d1-key-*")); len(left) != 0 {
		t.Errorf("key folder left behind: %v", left)
	}

	// The wrong key is refused.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherBlock, _ := ssh.MarshalPrivateKey(other, "")
	if _, err := b.Fetch(context.Background(), src, Auth{SSHKey: string(pem.EncodeToMemory(otherBlock))}, filepath.Join(parent, "builds", "d2"), nil); err == nil ||
		!strings.Contains(err.Error(), "refused the deploy key") {
		t.Errorf("clone with an unknown key: %v", err)
	}

	// A host key that changed since it was pinned is refused.
	fake, _, _ := ed25519.GenerateKey(rand.Reader)
	fakePub, _ := ssh.NewPublicKey(fake)
	if err := os.WriteFile(knownHosts, []byte("[127.0.0.1]:"+port+" "+strings.TrimSpace(string(ssh.MarshalAuthorizedKey(fakePub)))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = b.Fetch(context.Background(), src, auth, filepath.Join(parent, "builds", "d3"), nil)
	if err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Errorf("changed host key: %v", err)
	}

	// An https URL with a deploy key is refused before anything runs.
	if _, err := b.Fetch(context.Background(), store.GitSource{Repository: "https://example.com/a.git", Branch: "main"}, auth, filepath.Join(parent, "builds", "d4"), nil); err == nil {
		t.Error("https URL with a deploy key was accepted")
	}
}

func TestCommitMessage(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	long := strings.Repeat("x", maxMessage+50)
	git("commit", "-q", "--allow-empty", "-m", "Add paging", "-m", long)
	msg, err := (&Local{}).CommitMessage(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg, "Add paging\n\nxxx") || !strings.HasSuffix(msg, "…") || len([]rune(msg)) != maxMessage {
		t.Errorf("message = %q… (%d characters)", msg[:20], len([]rune(msg)))
	}
}
