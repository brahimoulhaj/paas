package executor

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// TestPrivateRegistry runs a registry with password auth in Docker, pushes an image to
// it, and checks login and pulls against it: wrong password refused, pull without
// credentials refused, pull with them works.
func TestPrivateRegistry(t *testing.T) {
	d := newTestDocker(t)
	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("docker", "run", "-d", "--rm", "-p", "127.0.0.1::5000",
		"-e", "HT=ci:"+string(hash),
		"-e", "REGISTRY_AUTH=htpasswd", "-e", "REGISTRY_AUTH_HTPASSWD_REALM=test", "-e", "REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd",
		"--entrypoint", "sh", "registry:2", "-c", `mkdir -p /auth && echo "$HT" > /auth/htpasswd && exec registry serve /etc/docker/registry/config.yml`).Output()
	if err != nil {
		t.Fatalf("start registry: %v", err)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", id).Run() })
	portOut, err := exec.Command("docker", "port", id, "5000/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimSpace(strings.Split(string(portOut), "\n")[0])
	_, port, _ := strings.Cut(addr, ":")
	server := "localhost:" + port
	deadline := time.Now().Add(time.Minute)
	for {
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("registry did not start")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Push a private image with a throwaway CLI config, so the user's logins are untouched.
	ref := server + "/private/busybox:1"
	cfg := filepath.Join(t.TempDir(), "docker-config")
	if err := d.PullImage(ctx, testImage, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--config", cfg, "login", server, "-u", "ci", "--password-stdin"},
		{"tag", testImage, ref},
		{"--config", cfg, "push", ref},
		{"image", "rm", ref},
	} {
		cmd := exec.Command("docker", args...)
		if args[2] == "login" {
			cmd.Stdin = strings.NewReader("s3cret")
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("docker %s: %v\n%s", args[0], err, out)
		}
	}
	t.Cleanup(func() { exec.Command("docker", "image", "rm", "-f", ref).Run() })

	if err := d.RegistryLogin(ctx, RegistryAuth{Server: server, Username: "ci", Password: "wrong"}); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("login with a wrong password = %v", err)
	}
	if err := d.RegistryLogin(ctx, RegistryAuth{Server: server, Username: "ci", Password: "s3cret"}); err != nil {
		t.Errorf("login = %v", err)
	}
	if err := d.PullImage(ctx, ref, nil, nil); err == nil {
		t.Error("pull without credentials worked")
	}
	if err := d.PullImage(ctx, ref, &RegistryAuth{Server: server, Username: "ci", Password: "s3cret"}, nil); err != nil {
		t.Errorf("pull with credentials = %v", err)
	}
}
