// Package builder turns a Git app's source into a local image: fetch the repository at a
// branch into a workspace, then build it. Both steps shell out to CLIs shipped in the
// panel image (git, docker with buildx), which talk to the same Docker daemon the
// executor uses.
package builder

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"panel/internal/brand"
	"panel/internal/store"
)

// LineFunc receives each output line of a fetch or a build.
type LineFunc func(line string)

// RailpackVersion is the Railpack release shipped in the panel image. The BuildKit frontend
// that turns its plan into an image must be the same version.
const RailpackVersion = "0.43.0"

// RailpackFrontend is the BuildKit frontend image for plans made by RailpackVersion.
const RailpackFrontend = "ghcr.io/railwayapp/railpack-frontend:v" + RailpackVersion

// BuildKitImage runs the capped builder (see Limits): the release buildx's own
// buildx-stable-1 tag pointed at when this was pinned.
const BuildKitImage = "moby/buildkit:v0.34.2"

// Limits caps the CPU and memory that builds use, all builds together. Zero leaves a
// resource uncapped; the zero value builds on the daemon's own BuildKit, as before.
type Limits struct {
	CPUs     float64
	MemoryMB int64
}

func (l Limits) capped() bool { return l.CPUs > 0 || l.MemoryMB > 0 }

// Local runs git, docker and railpack on the panel's own host.
type Local struct {
	Git      string // git binary; empty = "git"
	Docker   string // docker binary; empty = "docker"
	Railpack string // railpack binary; empty = "railpack"
	// RailpackFrontend overrides the BuildKit frontend image; empty = RailpackFrontend.
	RailpackFrontend string
	// Limits caps builds. BuildKit inside dockerd ignores per-build limits, so a capped
	// build runs on a buildx builder of its own: a BuildKit container created with the
	// limits (docker-container driver), sharing the same daemon.
	Limits Limits
	// LimitsFunc, when set, is asked for the limits at the start of every build (they can
	// change in Settings) and replaces Limits.
	LimitsFunc func(context.Context) (Limits, error)
	// BuildKitImage overrides the capped builder's image; empty = BuildKitImage.
	BuildKitImage string
	// KnownHosts is the known_hosts file of clones over SSH: a host's key is trusted the
	// first time and must match afterwards. Empty = ssh/known_hosts in the data dir, read
	// as the parent of the builds folder.
	KnownHosts string

	mu    sync.Mutex
	ready string         // name of the capped builder known to exist
	swept bool           // builders left by an earlier run were looked for
	inUse map[string]int // builds running on each capped builder
}

func (l *Local) limits(ctx context.Context) (Limits, error) {
	if l.LimitsFunc == nil {
		return l.Limits, nil
	}
	lim, err := l.LimitsFunc(ctx)
	if err != nil {
		return Limits{}, fmt.Errorf("read build limits: %w", err)
	}
	return lim, nil
}

// builderName names the capped builder after its settings, so changing the limits or the
// image makes a new builder instead of reusing one created with the old settings.
func (l *Local) builderName(lim Limits) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%g/%d/%s", lim.CPUs, lim.MemoryMB, l.buildkitImage()))
	return brand.Slug + "-build-" + hex.EncodeToString(sum[:4])
}

func (l *Local) buildkitImage() string {
	if l.BuildKitImage == "" {
		return BuildKitImage
	}
	return l.BuildKitImage
}

// ensureBuilder returns the --builder arguments for a heavy build step, and a release
// function to call once that step is over: no arguments when builds are not capped,
// otherwise the capped builder, created on first use. Capped builders made for other
// limits are removed with their cache once no build runs on them.
func (l *Local) ensureBuilder(ctx context.Context, line LineFunc) ([]string, func(), error) {
	lim, err := l.limits(ctx)
	if err != nil {
		return nil, nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !lim.capped() {
		if l.ready != "" || !l.swept {
			l.sweep(ctx, "")
			l.ready, l.swept = "", true
		}
		return nil, func() {}, nil
	}
	name := l.builderName(lim)
	if l.ready != name {
		found := l.sweep(ctx, name)
		l.swept = true
		if !found {
			say(line, "Creating the build container ("+lim.String()+")")
			args := []string{"buildx", "create", "--name", name, "--driver", "docker-container",
				"--driver-opt", "image=" + l.buildkitImage()}
			if lim.CPUs > 0 {
				args = append(args, "--driver-opt", "cpu-period=100000",
					"--driver-opt", fmt.Sprintf("cpu-quota=%d", int64(lim.CPUs*100000)))
			}
			if lim.MemoryMB > 0 {
				// Swap equal to memory: no swap, a build over the cap is killed instead of thrashing.
				m := fmt.Sprintf("%dm", lim.MemoryMB)
				args = append(args, "--driver-opt", "memory="+m, "--driver-opt", "memory-swap="+m)
			}
			args = append(args, "--bootstrap")
			if err := run(ctx, line, os.Environ(), l.docker(), args...); err != nil {
				_ = exec.Command(l.docker(), "buildx", "rm", "--force", name).Run()
				return nil, nil, fmt.Errorf("create build container: %w", err)
			}
		}
		l.ready = name
	}
	if l.inUse == nil {
		l.inUse = map[string]int{}
	}
	l.inUse[name]++
	release := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.inUse[name]--; l.inUse[name] <= 0 {
			delete(l.inUse, name)
		}
	}
	return []string{"--builder", name}, release, nil
}

// sweep removes the capped builders other than keep that no build is using, and reports
// whether keep exists. Called with l.mu held.
func (l *Local) sweep(ctx context.Context, keep string) bool {
	out, err := exec.CommandContext(ctx, l.docker(), "buildx", "ls", "--format", "{{.Name}}").Output()
	if err != nil {
		return false // creating keep will report a real problem
	}
	found := false
	for _, b := range strings.Fields(string(out)) {
		switch {
		case keep != "" && b == keep:
			found = true
		case strings.HasPrefix(b, brand.Slug+"-build-") && l.inUse[b] == 0:
			_ = exec.CommandContext(ctx, l.docker(), "buildx", "rm", "--force", b).Run()
		}
	}
	return found
}

// String describes the limits for the deployment log.
func (l Limits) String() string {
	var parts []string
	if l.CPUs > 0 {
		parts = append(parts, fmt.Sprintf("%g CPU", l.CPUs))
	}
	if l.MemoryMB > 0 {
		parts = append(parts, fmt.Sprintf("%d MB memory", l.MemoryMB))
	}
	if len(parts) == 0 {
		return "no limits"
	}
	return strings.Join(parts, ", ")
}

func (l *Local) railpack() string {
	if l.Railpack == "" {
		return "railpack"
	}
	return l.Railpack
}

func (l *Local) railpackFrontend() string {
	if l.RailpackFrontend == "" {
		return RailpackFrontend
	}
	return l.RailpackFrontend
}

func (l *Local) git() string {
	if l.Git == "" {
		return "git"
	}
	return l.Git
}

func (l *Local) docker() string {
	if l.Docker == "" {
		return "docker"
	}
	return l.Docker
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// Auth is the HTTP Basic credential for a private repository. The zero value means the
// repository is public.
type Auth struct {
	Username string
	Password string // an access token
	SSHKey   string // a deploy key (OpenSSH private key): the clone goes over SSH
}

// Fetch clones the tip of src.Branch into dir (which must not exist yet) and returns the
// commit it checked out. Only the one commit is downloaded. With a deploy key in auth the
// clone goes over SSH, otherwise over https.
func (l *Local) Fetch(ctx context.Context, src store.GitSource, auth Auth, dir string, line LineFunc) (string, error) {
	// No prompt for credentials (a private repository fails at once instead of hanging),
	// no credential helper from the host, and only the transport the URL was validated for.
	transport := "https"
	if auth.SSHKey != "" {
		transport = "ssh"
	}
	args := []string{
		"-c", "credential.helper=",
		"-c", "protocol.allow=never", "-c", "protocol." + transport + ".allow=always",
		"clone", "--depth", "1", "--single-branch", "--no-tags", "--branch", src.Branch,
		"--", src.Repository, dir,
	}
	var env []string
	var err error
	if auth.SSHKey != "" {
		var cleanup func()
		env, cleanup, err = l.sshEnv(src.Repository, auth.SSHKey, dir)
		if cleanup != nil {
			defer cleanup()
		}
	} else {
		env, err = gitEnv(src.Repository, auth)
	}
	if err != nil {
		return "", err
	}
	var seen []string
	collect := func(s string) {
		if len(seen) < 200 {
			seen = append(seen, s)
		}
		if line != nil {
			line(s)
		}
	}
	if err := run(ctx, collect, env, l.git(), args...); err != nil {
		if hint := sshHint(seen); auth.SSHKey != "" && hint != "" && ctx.Err() == nil {
			return "", fmt.Errorf("git clone: %s", hint)
		}
		return "", fmt.Errorf("git clone: %w", err)
	}
	out, err := exec.CommandContext(ctx, l.git(), "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	if !shaRe.MatchString(sha) {
		return "", fmt.Errorf("git rev-parse: unexpected output %q", sha)
	}
	return sha, nil
}

// sshHint explains the usual ways a clone over SSH fails, from ssh's and git's output.
// Git ends with a generic "make sure you have the correct access rights" whatever happened.
func sshHint(lines []string) string {
	all := strings.Join(lines, "\n")
	switch {
	case strings.Contains(all, "REMOTE HOST IDENTIFICATION HAS CHANGED") || strings.Contains(all, "Host key verification failed"):
		return "host key verification failed: the server's SSH host key changed since the first clone. " +
			"If that is expected, remove its line from ssh/known_hosts in the panel's data folder"
	case strings.Contains(all, "Permission denied (publickey"):
		return "the Git server refused the deploy key: add its public key to the repository (read access is enough)"
	case strings.Contains(all, "Could not resolve hostname"):
		return "could not resolve host: check the host name in the repository's SSH URL"
	case strings.Contains(all, "Connection refused") || strings.Contains(all, "Connection timed out") || strings.Contains(all, "Operation timed out"):
		return "could not reach the Git server over SSH (connection refused or timed out)"
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "fatal: ") || strings.HasPrefix(l, "ERROR: ") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// sshEnv is the environment of a clone over SSH with a deploy key. The key is written to a
// private folder next to the workspace (never inside the repository) and removed by
// cleanup. ssh reads no user or host configuration and no agent, never prompts, and
// trusts a host key the first time it sees it (kept in KnownHosts), refusing a changed one.
func (l *Local) sshEnv(repository, key, dir string) ([]string, func(), error) {
	if strings.HasPrefix(repository, "https://") || strings.HasPrefix(repository, "http://") {
		return nil, nil, errors.New("a deploy key clones over SSH: use the repository's SSH URL, like git@github.com:org/app.git")
	}
	knownHosts := l.KnownHosts
	if knownHosts == "" {
		knownHosts = filepath.Join(filepath.Dir(filepath.Dir(dir)), "ssh", "known_hosts")
	}
	for _, d := range []string{filepath.Dir(knownHosts), filepath.Dir(dir)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, nil, err
		}
	}
	keyDir, err := os.MkdirTemp(filepath.Dir(dir), filepath.Base(dir)+"-key-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(keyDir) }
	keyFile := filepath.Join(keyDir, "id")
	if !strings.HasSuffix(key, "\n") {
		key += "\n"
	}
	if err := os.WriteFile(keyFile, []byte(key), 0o600); err != nil {
		cleanup()
		return nil, nil, err
	}
	for _, p := range []string{keyFile, knownHosts} {
		if strings.ContainsAny(p, "'\"\\ \t\n") {
			cleanup()
			return nil, nil, fmt.Errorf("unsupported characters in the path %q", p)
		}
	}
	command := "ssh -F /dev/null -i " + keyFile + " -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes" +
		" -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=" + knownHosts + " -o ConnectTimeout=20"
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GIT_SSH_COMMAND="+command, "GIT_SSH_VARIANT=ssh")
	return env, cleanup, nil
}

// gitEnv is the environment of a clone. The credential travels as configuration in the
// environment, so it is neither on the command line (visible to every process) nor
// written to the workspace, and it is scoped to the repository's host: a redirect to
// another host does not receive it.
func gitEnv(repository string, auth Auth) ([]string, error) {
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GCM_INTERACTIVE=never")
	if auth == (Auth{}) {
		return env, nil
	}
	u, err := url.Parse(repository)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("a private repository needs an https URL")
	}
	basic := base64.StdEncoding.EncodeToString([]byte(auth.Username + ":" + auth.Password))
	return append(env,
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://"+u.Host+"/.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
	), nil
}

// Build builds the workspace in dir into the local image tag, with the builder the app
// chose. appID is recorded as an image label so the app's images can be found later.
func (l *Local) Build(ctx context.Context, src store.GitSource, dir, tag, appID string, line LineFunc) error {
	switch src.Builder {
	case store.BuilderDockerfile:
		return l.buildDockerfile(ctx, src, dir, tag, appID, line)
	case store.BuilderRailpack:
		return l.buildRailpack(ctx, src, dir, tag, appID, line)
	case store.BuilderAuto:
		if hasDockerfile(dir, src) {
			say(line, fmt.Sprintf("Found %s: building with the Dockerfile", displayPath(path.Join(src.BuildContext, src.Dockerfile))))
			return l.buildDockerfile(ctx, src, dir, tag, appID, line)
		}
		say(line, fmt.Sprintf("No %s: building with Railpack %s", displayPath(path.Join(src.BuildContext, src.Dockerfile)), RailpackVersion))
		return l.buildRailpack(ctx, src, dir, tag, appID, line)
	default:
		return fmt.Errorf("unknown builder %q", src.Builder)
	}
}

func (l *Local) buildDockerfile(ctx context.Context, src store.GitSource, dir, tag, appID string, line LineFunc) error {
	contextDir, err := inside(dir, src.BuildContext, true)
	if err != nil {
		return fmt.Errorf("build context %q: %w", displayPath(src.BuildContext), err)
	}
	dockerfile, err := inside(contextDir, src.Dockerfile, false)
	if err != nil {
		return fmt.Errorf("Dockerfile %q in the build context: %w", src.Dockerfile, err)
	}
	builder, release, err := l.ensureBuilder(ctx, line)
	if err != nil {
		return err
	}
	defer release()
	args := append([]string{"buildx", "build"}, builder...)
	args = append(args, "--progress=plain", "--load",
		"--tag", tag, "--file", dockerfile,
		"--label", brand.Slug+".build="+appID,
		contextDir,
	)
	if err := run(ctx, line, os.Environ(), l.docker(), args...); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	return nil
}

// hasDockerfile reports whether the app's Dockerfile exists inside the repository.
func hasDockerfile(dir string, src store.GitSource) bool {
	contextDir, err := inside(dir, src.BuildContext, true)
	if err != nil {
		return false
	}
	_, err = inside(contextDir, src.Dockerfile, false)
	return err == nil
}

func say(line LineFunc, s string) {
	if line != nil {
		line(s)
	}
}

// buildRailpack lets Railpack detect the language and plan the build, then builds the plan
// with Railpack's BuildKit frontend through buildx, on the same daemon as a Dockerfile.
// The frontend ignores --label, so the result is re-tagged through a one-line Dockerfile
// that adds the build label (metadata only, no new layer): pruning finds images by it.
func (l *Local) buildRailpack(ctx context.Context, src store.GitSource, dir, tag, appID string, line LineFunc) error {
	contextDir, err := inside(dir, src.BuildContext, true)
	if err != nil {
		return fmt.Errorf("build context %q: %w", displayPath(src.BuildContext), err)
	}
	// The plan lives next to the workspace, never inside the repository.
	work, err := os.MkdirTemp(filepath.Dir(dir), filepath.Base(dir)+"-railpack-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	plan := filepath.Join(work, "plan.json")
	if err := run(ctx, line, os.Environ(), l.railpack(), "prepare", contextDir,
		"--plan-out", plan, "--info-out", filepath.Join(work, "info.json")); err != nil {
		return fmt.Errorf("railpack: %w", err)
	}

	builder, release, err := l.ensureBuilder(ctx, line)
	if err != nil {
		return err
	}
	defer release()
	raw := tag + "-railpack"
	defer func() {
		// Untag the unlabelled image; its layers stay with the labelled one.
		_ = exec.Command(l.docker(), "image", "rm", raw).Run()
	}()
	args := append([]string{"buildx", "build"}, builder...)
	args = append(args, "--progress=plain", "--load",
		"--build-arg", "BUILDKIT_SYNTAX="+l.railpackFrontend(),
		"--file", plan, "--tag", raw, contextDir)
	if err := run(ctx, line, os.Environ(), l.docker(), args...); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}

	// The label step stays on the daemon's own builder: it starts FROM the image just
	// loaded, which only that builder sees, and adds metadata without running anything.
	empty := filepath.Join(work, "label")
	if err := os.Mkdir(empty, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(empty, "Dockerfile"), []byte("FROM "+raw+"\n"), 0o600); err != nil {
		return err
	}
	if err := run(ctx, line, os.Environ(), l.docker(), "buildx", "build", "--progress=plain", "--load",
		"--tag", tag, "--label", brand.Slug+".build="+appID, empty); err != nil {
		return fmt.Errorf("label image: %w", err)
	}
	return nil
}

func displayPath(p string) string {
	if p == "" {
		return "."
	}
	return p
}

// inside resolves rel against root and checks that the result, symlinks followed, is
// still inside root and is a directory (wantDir) or a regular file. A repository can hold
// symlinks; one must never point a build at the panel's own files.
func inside(root, rel string, wantDir bool) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	p, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("not found in the repository")
	}
	if err != nil {
		return "", err
	}
	if p != realRoot && !strings.HasPrefix(p, realRoot+string(filepath.Separator)) {
		return "", errors.New("points outside the repository")
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	switch {
	case wantDir && !fi.IsDir():
		return "", errors.New("is not a folder")
	case !wantDir && !fi.Mode().IsRegular():
		return "", errors.New("is not a file")
	}
	return p, nil
}

// run starts name with args, passes each output line (stdout and stderr together) to
// line, and on failure returns an error that ends with the last output line.
func run(ctx context.Context, line LineFunc, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return err
	}
	var last string
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			t := strings.TrimRight(sc.Text(), "\r")
			if t == "" {
				continue
			}
			last = t
			if line != nil {
				line(t)
			}
		}
		io.Copy(io.Discard, pr) // a line over the limit: keep draining so the process never blocks
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if last != "" {
		return fmt.Errorf("%w: %s", err, last)
	}
	return err
}
