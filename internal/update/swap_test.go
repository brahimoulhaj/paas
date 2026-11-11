package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
)

func TestRewriteComposeImage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.yaml")
	in := "name: berth\n\nservices:\n  berth:\n    image: ghcr.io/me/berth:0.1.0 # pinned\n    container_name: berth\n"
	if err := os.WriteFile(p, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rewriteComposeImage(p, "ghcr.io/me/berth:0.2.0"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	want := strings.Replace(in, "berth:0.1.0", "berth:0.2.0", 1)
	if string(b) != want {
		t.Fatalf("got\n%s\nwant\n%s", b, want)
	}
	if err := rewriteComposeImage(filepath.Join(dir, "missing.yaml"), "x"); err != nil {
		t.Fatalf("missing file: %v", err)
	}
}

func TestUserAliasesAndHealthURL(t *testing.T) {
	id := "0123456789abcdef0123"
	got := userAliases([]string{"berth", "0123456789ab", "panel"}, id)
	if strings.Join(got, ",") != "berth,panel" {
		t.Fatalf("aliases = %v", got)
	}
	if u := healthURL("berth", []string{"PATH=/bin"}); u != "http://berth:8080/api/health" {
		t.Fatalf("default = %s", u)
	}
	if u := healthURL("berth", []string{"BERTH_ADDR=:9090"}); u != "http://berth:9090/api/health" {
		t.Fatalf("custom port = %s", u)
	}
}

// The helper's result lands after the new panel started; Info picks it up and reports it once.
func TestInfoNoticesNewResultOnce(t *testing.T) {
	dir := t.TempDir()
	var got []Result
	s := New(Options{DataDir: dir, Version: "0.1.1", OnResult: func(r Result) { got = append(got, r) }})
	if s.Info().LastResult != nil || len(got) != 0 {
		t.Fatal("no result file yet")
	}
	if err := os.WriteFile(ResultPath(dir), []byte(`{"from":"a:0.1.0","to":"a:0.1.1","ok":true,"finished_at":"2026-11-11T07:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := s.Info().LastResult; r == nil || r.To != "a:0.1.1" {
		t.Fatalf("result = %+v", r)
	}
	s.Info()
	if len(got) != 1 || !got[0].OK {
		t.Fatalf("OnResult calls = %v", got)
	}
}

func TestContainerOwnConfig(t *testing.T) {
	img := &dockerspec.DockerOCIImageConfig{}
	img.Env = []string{"PATH=/usr/bin"}
	img.Entrypoint = []string{"/usr/local/bin/panel"}
	img.Cmd = []string{"serve"}
	img.Labels = map[string]string{"org.opencontainers.image.version": "0.1.0"}
	c := container.Config{
		Hostname:   "abc123",
		Env:        []string{"PATH=/usr/bin", "BERTH_PUBLIC_URL=http://1.2.3.4"},
		Entrypoint: []string{"/usr/local/bin/panel"},
		Cmd:        []string{"serve"},
		Labels:     map[string]string{"org.opencontainers.image.version": "0.1.0", "com.docker.compose.project": "berth"},
	}
	got := containerOwnConfig(c, img)
	if got.Hostname != "" || got.Entrypoint != nil || got.Cmd != nil {
		t.Fatalf("image-level fields kept: %+v", got)
	}
	if len(got.Env) != 1 || got.Env[0] != "BERTH_PUBLIC_URL=http://1.2.3.4" {
		t.Fatalf("env = %v", got.Env)
	}
	if len(got.Labels) != 1 || got.Labels["com.docker.compose.project"] != "berth" {
		t.Fatalf("labels = %v", got.Labels)
	}
	// An explicit override on the container survives.
	c.Cmd = []string{"serve", "--debug"}
	if got := containerOwnConfig(c, img); len(got.Cmd) != 2 {
		t.Fatalf("override dropped: %v", got.Cmd)
	}
}
