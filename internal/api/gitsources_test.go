package api

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"panel/internal/gitsource"
	"panel/internal/secret"
)

func TestGitSourcesAPI(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.call("GET", "/api/git-sources", nil); code != 401 {
		t.Errorf("signed out = %d, want 401", code)
	}
	e.signIn()
	e.must(503, "GET", "/api/git-sources", nil) // no service connected

	box, err := secret.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	e.srv.SetGitSources(gitsource.New(e.srv.store, box))

	if got := e.list("/api/git-sources"); len(got) != 0 {
		t.Fatalf("initial = %v", got)
	}
	e.must(422, "POST", "/api/git-sources/gitlab", map[string]any{"base_url": "http://gitlab.example", "token": "x"})
	e.must(422, "POST", "/api/git-sources/gitlab", map[string]any{"token": ""})
	e.must(404, "DELETE", "/api/git-sources/nope", nil)
	e.must(404, "GET", "/api/git-sources/nope/repos", nil)

	// Starting a GitHub connection returns what the browser posts to GitHub.
	e.must(422, "POST", "/api/git-sources/github", map[string]any{"origin": "not a url"})
	m := e.must(200, "POST", "/api/git-sources/github", map[string]any{"origin": "http://localhost:5173", "organization": "acme"})
	if !strings.HasPrefix(m["action_url"].(string), "https://github.com/organizations/acme/settings/apps/new?state=") ||
		!strings.Contains(m["manifest"].(string), `"redirect_url":"http://localhost:5173/api/git-sources/github/callback"`) {
		t.Errorf("manifest = %v", m)
	}

	// A callback that was not started here goes back to Settings with the reason.
	noFollow := *e.client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := noFollow.Get(e.url + "/api/git-sources/github/callback?code=abc&state=forged")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if loc := res.Header.Get("Location"); res.StatusCode != 302 || !strings.HasPrefix(loc, "/settings?tab=git&git_error=") {
		t.Errorf("forged callback = %d %q", res.StatusCode, loc)
	}

	// An app cannot name a connection that does not exist.
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "Shop"})["id"].(string)
	e.must(422, "POST", "/api/projects/"+pid+"/apps", map[string]any{
		"name": "api", "source_type": "git", "git_repository": "https://github.com/org/api.git", "port": 80, "git_source_id": "nope",
	})
}
