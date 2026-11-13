package gitsource

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"panel/internal/secret"
	"panel/internal/store"
)

func newService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, err := secret.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	return New(st, box), st
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func TestRepoOnHost(t *testing.T) {
	cases := []struct{ base, url, want string }{
		{"https://github.com", "https://github.com/org/app.git", "org/app"},
		{"https://github.com", "https://GitHub.com/org/app", "org/app"},
		{"https://gitlab.com", "https://gitlab.com/group/sub/app.git", "group/sub/app"},
		{"https://git.example.com/gitlab", "https://git.example.com/gitlab/group/app.git", "group/app"},
		{"https://github.com", "https://evil.example/org/app.git", ""},
		{"https://github.com", "https://github.com.evil.example/org/app.git", ""},
		{"https://github.com", "http://github.com/org/app.git", ""},
		{"https://github.com", "https://github.com/app", ""},
		{"https://github.com", "https://github.com/org/../app", ""},
	}
	for _, c := range cases {
		got, err := repoOnHost(c.base, c.url)
		if (c.want == "") != (err != nil) || got != c.want {
			t.Errorf("repoOnHost(%q, %q) = %q, %v; want %q", c.base, c.url, got, err, c.want)
		}
	}
}

func TestGitLab(t *testing.T) {
	const token = "glpat-secret-token"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v4/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != token {
			writeJSON(w, 401, map[string]string{"message": "401 Unauthorized"})
			return
		}
		if r.URL.Query().Get("membership") != "true" {
			t.Errorf("projects query = %s", r.URL.RawQuery)
		}
		writeJSON(w, 200, []map[string]any{
			{"path_with_namespace": "team/web", "http_url_to_repo": "https://gl/team/web.git", "default_branch": "main", "visibility": "private"},
			{"path_with_namespace": "team/Api", "http_url_to_repo": "https://gl/team/Api.git", "default_branch": "dev", "visibility": "public"},
		})
	})
	mux.HandleFunc("GET /api/v4/projects/{id}/repository/branches", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "team/web" {
			writeJSON(w, 404, map[string]string{"message": "404 Project Not Found"})
			return
		}
		writeJSON(w, 200, []map[string]string{{"name": "main"}, {"name": "release/2.0"}})
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	svc, st := newService(t)
	svc.http = srv.Client()
	ctx := context.Background()

	for _, bad := range [][2]string{{"http://gitlab.example", token}, {"ftp://x", token}, {srv.URL, ""}, {srv.URL, "has space"}, {"https://u:p@gitlab.example", token}} {
		if _, err := svc.AddGitLab(ctx, "", bad[0], bad[1]); !errors.Is(err, ErrInvalid) {
			t.Errorf("AddGitLab(%q, %q) = %v, want invalid", bad[0], bad[1], err)
		}
	}
	if _, err := svc.AddGitLab(ctx, "", srv.URL, "wrong"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "refused the token") {
		t.Errorf("wrong token: %v", err)
	}
	if list, _ := svc.List(ctx); len(list) != 0 {
		t.Fatalf("failed adds left connections: %+v", list)
	}

	c, err := svc.AddGitLab(ctx, "", srv.URL+"/", " "+token+" ")
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != store.GitTypeGitLab || c.BaseURL != srv.URL || c.Name != strings.TrimPrefix(srv.URL, "https://") {
		t.Errorf("connection = %+v", c)
	}
	row, _ := st.GetGitConnection(ctx, c.ID)
	if strings.Contains(row.Secret, token) {
		t.Error("the token is stored in the clear")
	}

	repos, err := svc.Repos(ctx, c.ID, "")
	if err != nil || len(repos) != 2 || repos[0].FullName != "team/Api" || repos[0].Private || !repos[1].Private || repos[1].DefaultBranch != "main" {
		t.Errorf("repos = %+v, %v", repos, err)
	}
	if repos, _ := svc.Repos(ctx, c.ID, "WEB"); len(repos) != 1 || repos[0].FullName != "team/web" {
		t.Errorf("filtered repos = %+v", repos)
	}
	branches, err := svc.Branches(ctx, c.ID, "team/web")
	if err != nil || strings.Join(branches, ",") != "main,release/2.0" {
		t.Errorf("branches = %v, %v", branches, err)
	}
	if _, err := svc.Branches(ctx, c.ID, "../etc"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad repo name: %v", err)
	}
	var ae *apiError
	if _, err := svc.Branches(ctx, c.ID, "team/none"); !errors.As(err, &ae) || ae.Status != 404 {
		t.Errorf("missing repo: %v", err)
	}

	auth, err := svc.Credentials(ctx, c.ID, srv.URL+"/team/web.git")
	if err != nil || auth.Username != "oauth2" || auth.Password != token {
		t.Errorf("credentials = %+v, %v", auth, err)
	}
	if _, err := svc.Credentials(ctx, c.ID, "https://evil.example/team/web.git"); err == nil {
		t.Error("credentials were offered for another host")
	}
	if err := svc.CheckRepository(ctx, c.ID, "https://evil.example/team/web.git"); !errors.Is(err, ErrInvalid) {
		t.Errorf("CheckRepository other host: %v", err)
	}
	if err := svc.CheckRepository(ctx, "nope", srv.URL+"/team/web.git"); !errors.Is(err, ErrInvalid) {
		t.Errorf("CheckRepository unknown connection: %v", err)
	}
	if err := svc.CheckRepository(ctx, c.ID, srv.URL+"/team/web.git"); err != nil {
		t.Errorf("CheckRepository: %v", err)
	}

	if err := svc.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Credentials(ctx, c.ID, srv.URL+"/team/web.git"); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Errorf("credentials after delete: %v", err)
	}
}

// fakeGitHub is the part of GitHub's API a GitHub App connection uses.
type fakeGitHub struct {
	t         *testing.T
	key       *rsa.PrivateKey
	pem       string
	tokenReqs atomic.Int32
}

// checkJWT verifies the app's signature and claims, like GitHub does.
func (g *fakeGitHub) checkJWT(r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
	if len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(&g.key.PublicKey, crypto.SHA256, sum[:], sig) != nil {
		return false
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	json.Unmarshal(raw, &claims)
	now := time.Now().Unix()
	return claims.Iss == "4242" && claims.Iat <= now && claims.Exp > now && claims.Exp-claims.Iat <= 600
}

func (g *fakeGitHub) handler() http.Handler {
	mux := http.NewServeMux()
	jwt := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !g.checkJWT(r) {
				writeJSON(w, 401, map[string]string{"message": "A JSON web token could not be decoded"})
				return
			}
			next(w, r)
		}
	}
	token := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer ghs_install7" {
				writeJSON(w, 401, map[string]string{"message": "Bad credentials"})
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("POST /app-manifests/{code}/conversions", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("code") != "goodcode" {
			writeJSON(w, 404, map[string]string{"message": "Not Found"})
			return
		}
		writeJSON(w, 201, map[string]any{
			"id": 4242, "slug": "berth-abc", "name": "berth-abc", "html_url": "https://github.com/apps/berth-abc",
			"client_id": "Iv1.x", "client_secret": "cs", "webhook_secret": "ws", "pem": g.pem,
		})
	})
	mux.HandleFunc("GET /app/installations", jwt(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, []map[string]any{{"id": 7}})
	}))
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", jwt(func(w http.ResponseWriter, r *http.Request) {
		g.tokenReqs.Add(1)
		writeJSON(w, 201, map[string]any{"token": "ghs_install" + r.PathValue("id"), "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	}))
	mux.HandleFunc("GET /installation/repositories", token(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"repositories": []map[string]any{
			{"full_name": "me/shop", "clone_url": "https://github.com/me/shop.git", "default_branch": "main", "private": true},
		}})
	}))
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", jwt(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("owner")+"/"+r.PathValue("repo") != "me/shop" {
			writeJSON(w, 404, map[string]string{"message": "Not Found"})
			return
		}
		writeJSON(w, 200, map[string]any{"id": 7})
	}))
	mux.HandleFunc("GET /repos/{owner}/{repo}/branches", token(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, []map[string]string{{"name": "main"}, {"name": "next"}})
	}))
	return mux
}

func TestGitHubApp(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	gh := &fakeGitHub{t: t, key: key, pem: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))}
	srv := httptest.NewServer(gh.handler())
	defer srv.Close()
	svc, st := newService(t)
	svc.GitHubAPI = srv.URL
	ctx := context.Background()

	// Start: what the browser posts to GitHub.
	for _, bad := range [][3]string{{"", "", ""}, {"ftp://panel", "", ""}, {"https://panel.example/x", "", ""}, {"https://panel.example", "bad org!", ""}, {"https://panel.example", "", strings.Repeat("n", 35)}} {
		if _, err := svc.StartGitHub(bad[0], bad[1], bad[2]); !errors.Is(err, ErrInvalid) {
			t.Errorf("StartGitHub(%q) = %v, want invalid", bad, err)
		}
	}
	m, err := svc.StartGitHub("https://panel.example.com/", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.ActionURL, "https://github.com/settings/apps/new?state=") || !strings.HasSuffix(m.ActionURL, m.State) || len(m.State) != 32 {
		t.Errorf("action url = %s", m.ActionURL)
	}
	var manifest struct {
		Name        string            `json:"name"`
		RedirectURL string            `json:"redirect_url"`
		SetupURL    string            `json:"setup_url"`
		Public      bool              `json:"public"`
		Permissions map[string]string `json:"default_permissions"`
	}
	if err := json.Unmarshal([]byte(m.Manifest), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.RedirectURL != "https://panel.example.com/api/git-sources/github/callback" || manifest.SetupURL != "https://panel.example.com/settings?tab=git" ||
		manifest.Public || !strings.HasPrefix(manifest.Name, "berth-") ||
		fmt.Sprint(manifest.Permissions) != "map[contents:read metadata:read]" {
		t.Errorf("manifest = %s", m.Manifest)
	}
	if org, _ := svc.StartGitHub("http://localhost:5173", "acme", "Acme deploys"); !strings.HasPrefix(org.ActionURL, "https://github.com/organizations/acme/settings/apps/new?state=") {
		t.Errorf("org action url = %s", org.ActionURL)
	}

	// Finish: the state is checked and used once.
	if _, err := svc.FinishGitHub(ctx, "goodcode", "not-a-state"); !errors.Is(err, ErrState) {
		t.Errorf("unknown state: %v", err)
	}
	var ae *apiError
	other, _ := svc.StartGitHub("https://panel.example.com", "", "")
	if _, err := svc.FinishGitHub(ctx, "badcode", other.State); !errors.As(err, &ae) || ae.Status != 404 {
		t.Errorf("bad code: %v", err)
	}
	c, err := svc.FinishGitHub(ctx, "goodcode", m.State)
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != store.GitTypeGitHubApp || c.Name != "berth-abc" || c.BaseURL != "https://github.com" ||
		c.InstallURL != "https://github.com/apps/berth-abc/installations/new" || c.AppURL != "https://github.com/apps/berth-abc" {
		t.Errorf("connection = %+v", c)
	}
	if _, err := svc.FinishGitHub(ctx, "goodcode", m.State); !errors.Is(err, ErrState) {
		t.Errorf("state reused: %v", err)
	}
	row, _ := st.GetGitConnection(ctx, c.ID)
	if strings.Contains(row.Secret, "PRIVATE KEY") || strings.Contains(row.Secret, "Iv1.x") {
		t.Error("the app's keys are stored in the clear")
	}

	// Repositories, branches and clone credentials go through installation tokens.
	repos, err := svc.Repos(ctx, c.ID, "")
	if err != nil || len(repos) != 1 || repos[0].FullName != "me/shop" || !repos[0].Private || repos[0].CloneURL != "https://github.com/me/shop.git" {
		t.Errorf("repos = %+v, %v", repos, err)
	}
	branches, err := svc.Branches(ctx, c.ID, "me/shop")
	if err != nil || strings.Join(branches, ",") != "main,next" {
		t.Errorf("branches = %v, %v", branches, err)
	}
	auth, err := svc.Credentials(ctx, c.ID, "https://github.com/me/shop.git")
	if err != nil || auth.Username != "x-access-token" || auth.Password != "ghs_install7" {
		t.Errorf("credentials = %+v, %v", auth, err)
	}
	if n := gh.tokenReqs.Load(); n != 1 {
		t.Errorf("installation tokens requested %d times, want 1 (cached)", n)
	}
	if _, err := svc.Credentials(ctx, c.ID, "https://github.com/me/other.git"); err == nil || !strings.Contains(err.Error(), "not installed on me/other") {
		t.Errorf("repository outside the installation: %v", err)
	}
	if _, err := svc.Credentials(ctx, c.ID, "https://gitlab.com/me/shop.git"); err == nil {
		t.Error("credentials were offered for another host")
	}
}

func TestDeleteRefusedWhileInUse(t *testing.T) {
	svc, st := newService(t)
	ctx := context.Background()
	c, err := svc.save(ctx, store.GitTypeGitLab, "gl", "https://gitlab.com", creds{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := st.CreateProject(ctx, "Shop", "")
	app, err := st.CreateApp(ctx, p.ID, store.AppInput{Name: "web", SourceType: store.SourceGit, Port: 80,
		Git: store.GitSource{ConnectionID: c.ID, Repository: "https://gitlab.com/a/b.git", Branch: "main", Builder: store.BuilderDockerfile, Dockerfile: "Dockerfile"}})
	if err != nil || app.Git.ConnectionID != c.ID {
		t.Fatalf("app = %+v, %v", app, err)
	}
	if err := svc.Delete(ctx, c.ID); !errors.Is(err, store.ErrConflict) {
		t.Errorf("delete while in use: %v", err)
	}
	if err := st.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, c.ID); err != nil {
		t.Errorf("delete: %v", err)
	}
	if err := svc.Delete(ctx, c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete again: %v", err)
	}
}
