package gitsource

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"panel/internal/store"
)

func TestDeployKey(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()

	c, err := svc.AddDeployKey(ctx, "  web repo  ")
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != store.GitTypeDeployKey || c.Name != "web repo" || c.BaseURL != "" || !strings.HasPrefix(c.PublicKey, "ssh-ed25519 ") || !strings.HasSuffix(c.PublicKey, " web-repo") {
		t.Fatalf("connection = %+v", c)
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(c.PublicKey)); err != nil {
		t.Errorf("public key does not parse: %v", err)
	}
	// The private key is stored encrypted, and is the half of the public key shown.
	row, _ := st.GetGitConnection(ctx, c.ID)
	if strings.Contains(row.Secret, "PRIVATE KEY") {
		t.Error("private key stored in clear")
	}
	auth, err := svc.Credentials(ctx, c.ID, "git@github.com:org/app.git")
	if err != nil || auth.Username != "" || auth.Password != "" {
		t.Fatalf("credentials = %+v, %v", auth, err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(auth.SSHKey))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))); !strings.HasPrefix(c.PublicKey, got+" ") {
		t.Errorf("private key does not match the public key: %s vs %s", got, c.PublicKey)
	}
	if list, _ := svc.List(ctx); len(list) != 1 || list[0].PublicKey != c.PublicKey {
		t.Errorf("list = %+v", list)
	}

	// SSH URLs only; no repository or branch listing.
	if _, err := svc.Credentials(ctx, c.ID, "https://github.com/org/app.git"); err == nil {
		t.Error("credentials for an https URL")
	}
	if err := svc.CheckRepository(ctx, c.ID, "ssh://git@git.example.com:2222/org/app.git"); err != nil {
		t.Errorf("check ssh:// = %v", err)
	}
	if err := svc.CheckRepository(ctx, c.ID, "https://github.com/org/app.git"); !errors.Is(err, ErrInvalid) {
		t.Errorf("check https = %v", err)
	}
	if _, err := svc.Repos(ctx, c.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("repos = %v", err)
	}
	if _, err := svc.Branches(ctx, c.ID, "org/app"); !errors.Is(err, ErrInvalid) {
		t.Errorf("branches = %v", err)
	}
	if _, err := svc.AddDeployKey(ctx, strings.Repeat("x", 65)); !errors.Is(err, ErrInvalid) {
		t.Errorf("long name = %v", err)
	}
}

func TestSSHRepo(t *testing.T) {
	for raw, want := range map[string]bool{
		"git@github.com:org/app.git":                 true,
		"git@gitlab.example.com:group/sub/app.git":   true,
		"ssh://git@git.example.com/org/app.git":      true,
		"ssh://git@git.example.com:2222/org/app.git": true,
		"ssh://git@127.0.0.1:2222/home/git/app.git":  true,
		"github.com:org/app.git":                     false, // no user
		"git@-oProxyCommand=x:org/app.git":           false,
		"git@github.com:-upload-pack=x":              false,
		"ssh://git@-oProxyCommand=x/org/app.git":     false,
		"ssh://git:secret@github.com/org/app.git":    false,
		"ssh://git@github.com:99999/org/app.git":     false,
		"ssh://git@github.com/":                      false,
		"git@github.com:org/../../etc.git":           false,
		"https://github.com/org/app.git":             false,
		"git@github.com:org/app.git extra":           false,
		"ext::sh -c touch% /tmp/pwned":               false,
	} {
		if got := SSHRepo(raw); got != want {
			t.Errorf("SSHRepo(%q) = %v, want %v", raw, got, want)
		}
	}
}
