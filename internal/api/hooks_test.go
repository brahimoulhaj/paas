package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// hook calls a deploy webhook the way a Git host or CI does: no cookie, maybe event
// headers, a JSON body.
func (e *env) hook(token string, headers map[string]string, body string) (int, map[string]any) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.url+"/api/hooks/deploy/"+token, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

func TestDeployHook(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "Shop"})["id"].(string)
	app := e.must(201, "POST", "/api/projects/"+pid+"/apps", map[string]any{"name": "web", "image": "ghcr.io/org/web:1", "port": 80})
	id := app["id"].(string)
	if app["deploy_hook"] != false {
		t.Fatalf("hook on by default: %v", app)
	}
	if h := e.must(200, "GET", "/api/apps/"+id+"/deploy-hook", nil); h["enabled"] != false || h["token"] != nil {
		t.Fatalf("hook = %v", h)
	}
	token := e.must(200, "POST", "/api/apps/"+id+"/deploy-hook", nil)["token"].(string)
	if len(token) != 48 || e.must(200, "GET", "/api/apps/"+id, nil)["deploy_hook"] != true {
		t.Fatalf("token %q", token)
	}
	if code, _ := e.hook("not-a-token", nil, ""); code != 404 {
		t.Errorf("unknown token = %d", code)
	}

	// A CI call deploys; with a tag it switches the image's tag first.
	if code, m := e.hook(token, nil, ""); code != 202 || m["status"] != "deploying" {
		t.Fatalf("call = %d %v", code, m)
	}
	e.deployer.Wait()
	if code, m := e.hook(token, nil, `{"tag":"2.0.1"}`); code != 202 {
		t.Fatalf("tag = %d %v", code, m)
	}
	e.deployer.Wait()
	if a := e.must(200, "GET", "/api/apps/"+id, nil); a["image"] != "ghcr.io/org/web:2.0.1" {
		t.Errorf("image after tag = %v", a["image"])
	}
	if code, _ := e.hook(token, nil, `{"tag":"../x"}`); code != 422 {
		t.Errorf("bad tag = %d", code)
	}
	if code, _ := e.hook(token, nil, `not json`); code != 422 {
		t.Errorf("bad body = %d", code)
	}
	// An image app ignores Git pushes.
	if code, m := e.hook(token, map[string]string{"X-GitHub-Event": "push"}, `{"ref":"refs/heads/main"}`); code != 200 || m["status"] != "ignored" {
		t.Errorf("push to image app = %d %v", code, m)
	}

	// Pushes while a deploy runs queue one more deploy, however many arrive.
	e.ex.PullDelay = 300 * time.Millisecond
	statuses := []any{}
	for range 3 {
		_, m := e.hook(token, nil, "")
		statuses = append(statuses, m["status"])
	}
	if statuses[0] != "deploying" || statuses[1] != "queued" || statuses[2] != "queued" {
		t.Errorf("statuses = %v", statuses)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(e.list("/api/apps/"+id+"/deployments")) < 4 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	e.deployer.Wait()
	e.ex.PullDelay = 0
	if deps := e.list("/api/apps/" + id + "/deployments"); len(deps) != 4 || deps[0]["trigger"] != "webhook" {
		t.Errorf("deployments after a burst = %d (want 2 + 2): %v", len(deps), deps[0]["trigger"])
	}

	// Git apps: only pushes to their branch deploy.
	e.must(200, "PATCH", "/api/apps/"+id, map[string]any{"source_type": "git", "git_repository": "https://github.com/org/web.git", "git_branch": "main"})
	for _, c := range []struct {
		headers map[string]string
		body    string
		code    int
		status  string
	}{
		{map[string]string{"X-GitHub-Event": "ping"}, `{"zen":"hi"}`, 200, "ok"},
		{map[string]string{"X-GitHub-Event": "push"}, `{"ref":"refs/heads/dev"}`, 200, "ignored"},
		{map[string]string{"X-GitHub-Event": "push"}, `{"ref":"refs/tags/v1"}`, 200, "ignored"},
		{map[string]string{"X-GitHub-Event": "push"}, `{"ref":"refs/heads/main","deleted":true,"after":"0000000000000000000000000000000000000000"}`, 200, "ignored"},
		{map[string]string{"X-GitHub-Event": "issues"}, `{}`, 200, "ignored"},
		{map[string]string{"X-GitHub-Event": "push"}, `{"ref":"refs/heads/main","after":"abc"}`, 202, "deploying"},
		{map[string]string{"X-Gitlab-Event": "Push Hook"}, `{"ref":"refs/heads/main"}`, 202, "deploying"},
		{map[string]string{"X-Gitea-Event": "push"}, `{"ref":"refs/heads/main"}`, 202, "deploying"},
		{nil, `{"ref":"refs/heads/main"}`, 202, "deploying"},
		{map[string]string{"X-GitHub-Event": "push", "Content-Type": "application/x-www-form-urlencoded"},
			"payload=" + url.QueryEscape(`{"ref":"refs/heads/main"}`), 202, "deploying"},
	} {
		code, m := e.hook(token, c.headers, c.body)
		if code != c.code || m["status"] != c.status {
			t.Errorf("%v %s = %d %v, want %d %s", c.headers, c.body, code, m, c.code, c.status)
		}
		e.deployer.Wait()
	}
	if code, _ := e.hook(token, nil, `{"tag":"3"}`); code != 422 {
		t.Errorf("tag on a git app = %d", code)
	}

	// A new URL replaces the old one; turning it off stops it.
	newToken := e.must(200, "POST", "/api/apps/"+id+"/deploy-hook", nil)["token"].(string)
	if code, _ := e.hook(token, nil, ""); code != 404 || newToken == token {
		t.Errorf("old token after regenerate = %d", code)
	}
	e.must(200, "DELETE", "/api/apps/"+id+"/deploy-hook", nil)
	if code, _ := e.hook(newToken, nil, ""); code != 404 {
		t.Errorf("token after off = %d", code)
	}
	found := false
	for _, ev := range e.list("/api/events") {
		if ev["actor"] == "webhook" && ev["action"] == "app.deploy" {
			found = true
		}
	}
	if !found {
		t.Error("no webhook event in the activity")
	}
}

func TestWithTag(t *testing.T) {
	for in, want := range map[string]string{
		"nginx":                           "nginx:2",
		"nginx:1":                         "nginx:2",
		"ghcr.io/org/app:1":               "ghcr.io/org/app:2",
		"registry.example.com:5000/app":   "registry.example.com:5000/app:2",
		"registry.example.com:5000/app:1": "registry.example.com:5000/app:2",
		"ghcr.io/org/app@sha256:abc":      "ghcr.io/org/app:2",
		"ghcr.io/org/app:1@sha256:abc":    "ghcr.io/org/app:2",
	} {
		if got := withTag(in, "2"); got != want {
			t.Errorf("withTag(%q) = %q, want %q", in, got, want)
		}
	}
}
