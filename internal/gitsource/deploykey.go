package gitsource

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"

	"panel/internal/store"
)

// AddDeployKey generates an SSH key pair for cloning over SSH. The owner adds the public
// key to a repository (GitHub, GitLab, Gitea, any Git server) as a read-only deploy key;
// apps using the connection give the repository's SSH URL. Works with any host, so the
// connection has no base URL.
func (s *Service) AddDeployKey(ctx context.Context, name string) (Connection, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Deploy key"
	}
	if len(name) > 64 {
		return Connection{}, invalidf("name must be at most 64 characters")
	}
	private, public, err := newDeployKey(name)
	if err != nil {
		return Connection{}, err
	}
	return s.save(ctx, store.GitTypeDeployKey, name, "", creds{PrivateKey: private, PublicKey: public})
}

// newDeployKey returns an ed25519 key pair: the private key in OpenSSH PEM form and the
// public key as an authorized_keys line, commented with name.
func newDeployKey(name string) (string, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	comment := strings.Map(func(r rune) rune {
		if r < 0x21 || r > 0x7e {
			return '-'
		}
		return r
	}, name)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment
	return string(pem.EncodeToMemory(block)), line, nil
}

var (
	// scp-like: user@host:path. No scheme, a user, a host that cannot be read as an option.
	scpRe  = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9][A-Za-z0-9.-]*):([A-Za-z0-9._~/-]+)$`)
	hostRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
)

// SSHRepo reports whether raw is an SSH repository URL a deploy key can clone:
// git@host:org/app.git or ssh://git@host[:port]/org/app.git.
func SSHRepo(raw string) bool {
	if len(raw) > 500 || strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	if m := scpRe.FindStringSubmatch(raw); m != nil {
		return validSSHPath(m[3])
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "ssh" || u.User == nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if _, hasPassword := u.User.Password(); hasPassword || !hostRe.MatchString(u.Hostname()) {
		return false
	}
	if p := u.Port(); p != "" {
		var n int
		if _, err := fmt.Sscanf(p, "%d", &n); err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return validSSHPath(strings.TrimPrefix(u.Path, "/"))
}

func validSSHPath(p string) bool {
	return p != "" && !strings.HasPrefix(p, "-") && !strings.Contains(p, "..") && strings.Trim(p, "/") != ""
}
