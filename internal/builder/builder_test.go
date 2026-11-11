package builder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
