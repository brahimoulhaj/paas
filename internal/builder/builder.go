// Package builder turns a Git app's source into a local image: fetch the repository at a
// branch into a workspace, then build it. Both steps shell out to CLIs shipped in the
// panel image (git, docker with buildx), which talk to the same Docker daemon the
// executor uses.
package builder

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"panel/internal/brand"
	"panel/internal/store"
)

// LineFunc receives each output line of a fetch or a build.
type LineFunc func(line string)

// Local runs git and docker on the panel's own host.
type Local struct {
	Git    string // git binary; empty = "git"
	Docker string // docker binary; empty = "docker"
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
}

// Fetch clones the tip of src.Branch into dir (which must not exist yet) and returns the
// commit it checked out. Only the one commit is downloaded.
func (l *Local) Fetch(ctx context.Context, src store.GitSource, auth Auth, dir string, line LineFunc) (string, error) {
	// No prompt for credentials (a private repository fails at once instead of hanging),
	// no credential helper from the host, and only the transport the URL was validated for.
	args := []string{
		"-c", "credential.helper=",
		"-c", "protocol.allow=never", "-c", "protocol.https.allow=always",
		"clone", "--depth", "1", "--single-branch", "--no-tags", "--branch", src.Branch,
		"--", src.Repository, dir,
	}
	env, err := gitEnv(src.Repository, auth)
	if err != nil {
		return "", err
	}
	if err := run(ctx, line, env, l.git(), args...); err != nil {
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
	args := []string{
		"buildx", "build", "--progress=plain", "--load",
		"--tag", tag, "--file", dockerfile,
		"--label", brand.Slug + ".build=" + appID,
		contextDir,
	}
	if err := run(ctx, line, os.Environ(), l.docker(), args...); err != nil {
		return fmt.Errorf("docker build: %w", err)
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
