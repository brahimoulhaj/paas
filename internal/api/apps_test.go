package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// raw sends a request and returns the undecoded body (list and text endpoints).
func (e *env) raw(method, path string, body any) (int, []byte) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.url+path, r)
	res, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func (e *env) list(path string) []map[string]any {
	e.t.Helper()
	code, body := e.raw("GET", path, nil)
	var out []map[string]any
	if err := json.Unmarshal(body, &out); code != 200 || err != nil {
		e.t.Fatalf("GET %s = %d %s (%v)", path, code, body, err)
	}
	return out
}

// must sends a request and fails the test unless the status matches.
func (e *env) must(want int, method, path string, body any) map[string]any {
	e.t.Helper()
	code, m := e.call(method, path, body)
	if code != want {
		e.t.Fatalf("%s %s = %d %v, want %d", method, path, code, m, want)
	}
	return m
}

func TestAppsRequireAuth(t *testing.T) {
	e := newEnv(t)
	for _, c := range [][2]string{
		{"GET", "/api/projects"}, {"POST", "/api/projects"}, {"GET", "/api/projects/x"}, {"DELETE", "/api/projects/x"},
		{"GET", "/api/apps/x"}, {"POST", "/api/apps/x/deploy"}, {"POST", "/api/apps/x/stop"}, {"DELETE", "/api/apps/x"},
		{"POST", "/api/apps/x/domains"}, {"DELETE", "/api/domains/x"}, {"GET", "/api/deployments/x/log"}, {"GET", "/api/events"},
	} {
		if code, _ := e.call(c[0], c[1], map[string]any{}); code != 401 {
			t.Errorf("%s %s signed out = %d, want 401", c[0], c[1], code)
		}
	}
}

func TestProjectsAPI(t *testing.T) {
	e := newEnv(t)
	e.signIn()

	if got := e.list("/api/projects"); len(got) != 0 {
		t.Fatalf("initial projects = %v", got)
	}
	for _, bad := range []map[string]any{{"name": ""}, {"name": "   "}, {"name": strings.Repeat("x", 65)}, {"name": "ok", "description": strings.Repeat("d", 501)}} {
		e.must(422, "POST", "/api/projects", bad)
	}
	p := e.must(201, "POST", "/api/projects", map[string]any{"name": " Shop ", "description": "the shop"})
	id := p["id"].(string)
	if p["name"] != "Shop" || p["app_count"] != float64(0) {
		t.Fatalf("created = %v", p)
	}
	e.must(409, "POST", "/api/projects", map[string]any{"name": "shop"})
	e.must(404, "GET", "/api/projects/missing", nil)

	// PATCH changes only what is sent.
	p = e.must(200, "PATCH", "/api/projects/"+id, map[string]any{"name": "Store"})
	if p["name"] != "Store" || p["description"] != "the shop" {
		t.Fatalf("patched = %v", p)
	}
	e.must(422, "PATCH", "/api/projects/"+id, map[string]any{"name": ""})
	e.must(404, "PATCH", "/api/projects/missing", map[string]any{"name": "x"})

	app := e.must(201, "POST", "/api/projects/"+id+"/apps", map[string]any{"name": "web", "image": "nginx:1", "port": 80})
	if c := e.must(409, "DELETE", "/api/projects/"+id, nil); !strings.Contains(c["error"].(map[string]any)["message"].(string), "apps first") {
		t.Errorf("delete with apps = %v", c)
	}
	if code, _ := e.raw("DELETE", "/api/apps/"+app["id"].(string), nil); code != 204 {
		t.Fatalf("delete app = %d", code)
	}
	if code, _ := e.raw("DELETE", "/api/projects/"+id, nil); code != 204 {
		t.Fatalf("delete project = %d", code)
	}
	e.must(404, "GET", "/api/projects/"+id, nil)

	var actions []string
	for _, ev := range e.list("/api/events") {
		actions = append(actions, ev["action"].(string))
		if ev["actor"] != "a@b.co" {
			t.Errorf("event actor = %v", ev["actor"])
		}
	}
	if got := strings.Join(actions, ","); got != "project.delete,app.delete,app.create,project.update,project.create" {
		t.Errorf("events = %s", got)
	}
	e.must(422, "GET", "/api/events?limit=0", nil)
	if got := e.list("/api/events?limit=2"); len(got) != 2 {
		t.Errorf("limit=2 returned %d", len(got))
	}
}

func TestAppsAPI(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "Shop"})["id"].(string)
	apps := "/api/projects/" + pid + "/apps"

	for name, bad := range map[string]map[string]any{
		"uppercase name":   {"name": "Web", "image": "nginx:1", "port": 80},
		"leading dash":     {"name": "-web", "image": "nginx:1", "port": 80},
		"empty image":      {"name": "web", "image": "", "port": 80},
		"image with flag":  {"name": "web", "image": "--privileged", "port": 80},
		"image with space": {"name": "web", "image": "nginx 1", "port": 80},
		"port zero":        {"name": "web", "image": "nginx:1", "port": 0},
		"port too high":    {"name": "web", "image": "nginx:1", "port": 70000},
		"negative cpu":     {"name": "web", "image": "nginx:1", "port": 80, "cpu_limit": -1},
		"tiny memory":      {"name": "web", "image": "nginx:1", "port": 80, "memory_limit_mb": 2},
	} {
		if code, m := e.call("POST", apps, bad); code != 422 {
			t.Errorf("%s = %d %v, want 422", name, code, m)
		}
	}
	e.must(404, "POST", "/api/projects/missing/apps", map[string]any{"name": "web", "image": "nginx:1", "port": 80})

	a := e.must(201, "POST", apps, map[string]any{"name": "web", "image": "ghcr.io/org/app:1.2", "port": 3000, "cpu_limit": 0.5, "memory_limit_mb": 256})
	id := a["id"].(string)
	if a["source_type"] != "image" || a["desired_state"] != "stopped" || a["current_deployment_id"] != "" || a["memory_limit_mb"] != float64(256) {
		t.Fatalf("created = %v", a)
	}
	e.must(409, "POST", apps, map[string]any{"name": "web", "image": "nginx:1", "port": 80})
	if got := e.list(apps); len(got) != 1 || got[0]["id"] != id {
		t.Fatalf("list = %v", got)
	}
	if got := e.must(200, "GET", "/api/apps/"+id, nil); got["runtime_state"] != "none" {
		t.Errorf("runtime_state before deploy = %v", got["runtime_state"])
	}

	a = e.must(200, "PATCH", "/api/apps/"+id, map[string]any{"image": "ghcr.io/org/app:1.3"})
	if a["image"] != "ghcr.io/org/app:1.3" || a["port"] != float64(3000) || a["cpu_limit"] != 0.5 {
		t.Fatalf("patched = %v", a)
	}
	e.must(422, "PATCH", "/api/apps/"+id, map[string]any{"port": -1})
	e.must(404, "PATCH", "/api/apps/missing", map[string]any{"port": 80})

	// Runtime actions before any deploy.
	for _, action := range []string{"start", "stop", "restart"} {
		if m := e.must(409, "POST", "/api/apps/"+id+"/"+action, nil); m["error"].(map[string]any)["code"] != "not_deployed" {
			t.Errorf("%s before deploy = %v", action, m)
		}
	}
	e.must(404, "POST", "/api/apps/missing/deploy", nil)

	// Deploy: 202 with a queued record, then it succeeds in the background.
	dep := e.must(202, "POST", "/api/apps/"+id+"/deploy", nil)
	depID := dep["id"].(string)
	if dep["status"] != "queued" || dep["image"] != "ghcr.io/org/app:1.3" || dep["started_at"] != nil {
		t.Fatalf("deploy response = %v", dep)
	}
	e.deployer.Wait()
	dep = e.must(200, "GET", "/api/deployments/"+depID, nil)
	if dep["status"] != "succeeded" || dep["container_name"] == "" || dep["finished_at"] == nil || dep["port"] != float64(3000) {
		t.Fatalf("finished deployment = %v", dep)
	}
	if got := e.list("/api/apps/" + id + "/deployments"); len(got) != 1 || got[0]["id"] != depID {
		t.Fatalf("deployments = %v", got)
	}
	code, logText := e.raw("GET", "/api/deployments/"+depID+"/log", nil)
	if code != 200 || !strings.Contains(string(logText), "Deployment succeeded") {
		t.Fatalf("log = %d %q", code, logText)
	}
	e.must(404, "GET", "/api/deployments/missing/log", nil)
	a = e.must(200, "GET", "/api/apps/"+id, nil)
	if a["runtime_state"] != "running" || a["desired_state"] != "running" || a["current_deployment_id"] != depID {
		t.Fatalf("app after deploy = %v", a)
	}

	// Stop / start / restart.
	if a = e.must(200, "POST", "/api/apps/"+id+"/stop", nil); a["runtime_state"] != "exited" || a["desired_state"] != "stopped" {
		t.Errorf("after stop = %v", a)
	}
	if a = e.must(200, "POST", "/api/apps/"+id+"/start", nil); a["runtime_state"] != "running" || a["desired_state"] != "running" {
		t.Errorf("after start = %v", a)
	}
	if a = e.must(200, "POST", "/api/apps/"+id+"/restart", nil); a["runtime_state"] != "running" {
		t.Errorf("after restart = %v", a)
	}

	// A failing deploy reports why and leaves the app running.
	e.must(200, "PATCH", "/api/apps/"+id, map[string]any{"image": "registry.example/missing:1"})
	failed := e.must(202, "POST", "/api/apps/"+id+"/deploy", nil)["id"].(string)
	e.deployer.Wait()
	if dep = e.must(200, "GET", "/api/deployments/"+failed, nil); dep["status"] != "failed" || !strings.Contains(dep["error"].(string), "manifest unknown") {
		t.Fatalf("failed deployment = %v", dep)
	}
	if a = e.must(200, "GET", "/api/apps/"+id, nil); a["runtime_state"] != "running" || a["current_deployment_id"] != depID {
		t.Errorf("app after failed deploy = %v", a)
	}

	// Delete removes the container too.
	if code, _ := e.raw("DELETE", "/api/apps/"+id, nil); code != 204 {
		t.Fatalf("delete = %d", code)
	}
	if cs, _ := e.ex.ListContainers(context.Background()); len(cs) != 0 {
		t.Errorf("containers after delete = %+v", cs)
	}
	e.must(404, "GET", "/api/apps/"+id, nil)
	e.must(404, "GET", "/api/deployments/"+depID, nil)
}

func TestDeployBusy(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "Shop"})["id"].(string)
	id := e.must(201, "POST", "/api/projects/"+pid+"/apps", map[string]any{"name": "web", "image": "nginx:1", "port": 80})["id"].(string)
	e.ex.PullDelay = 200_000_000 // 200 ms
	e.must(202, "POST", "/api/apps/"+id+"/deploy", nil)
	if m := e.must(409, "POST", "/api/apps/"+id+"/deploy", nil); m["error"].(map[string]any)["code"] != "busy" {
		t.Errorf("second deploy = %v", m)
	}
	e.must(409, "DELETE", "/api/apps/"+id, nil)
	e.deployer.Wait()
}

func TestDomainsAPI(t *testing.T) {
	e := newEnv(t)
	px := &fakeProxy{}
	e.srv.SetProxy(px)
	e.signIn()
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "Shop"})["id"].(string)
	id := e.must(201, "POST", "/api/projects/"+pid+"/apps", map[string]any{"name": "web", "image": "nginx:1", "port": 80})["id"].(string)
	other := e.must(201, "POST", "/api/projects/"+pid+"/apps", map[string]any{"name": "api", "image": "nginx:1", "port": 80})["id"].(string)
	domains := "/api/apps/" + id + "/domains"

	for _, bad := range []string{"", "nodots", "https://shop.example.com", "shop.example.com/path", "*.example.com", "10.0.0.1", "shop.example.com:8443"} {
		e.must(422, "POST", domains, map[string]any{"hostname": bad})
	}
	e.must(404, "POST", "/api/apps/missing/domains", map[string]any{"hostname": "shop.example.com"})
	if px.applied != 0 {
		t.Fatalf("rejected input reached the proxy %d times", px.applied)
	}

	d := e.must(201, "POST", domains, map[string]any{"hostname": " Shop.Example.com "})
	if d["hostname"] != "shop.example.com" || d["tls_mode"] != "auto" || px.applied != 1 {
		t.Fatalf("created = %v, applied %d", d, px.applied)
	}
	e.must(409, "POST", domains, map[string]any{"hostname": "shop.example.com"})
	e.must(409, "POST", "/api/apps/"+other+"/domains", map[string]any{"hostname": "shop.example.com"})

	// TLS mode: HTTP only for a CDN that terminates TLS, switchable both ways.
	e.must(422, "POST", domains, map[string]any{"hostname": "cf.example.com", "tls_mode": "nope"})
	cf := e.must(201, "POST", domains, map[string]any{"hostname": "cf.example.com", "tls_mode": "http_only"})
	if cf["tls_mode"] != "http_only" {
		t.Fatalf("http-only domain = %v", cf)
	}
	modeBase := px.applied
	e.must(422, "PATCH", "/api/domains/"+cf["id"].(string), map[string]any{"tls_mode": ""})
	if m := e.must(200, "PATCH", "/api/domains/"+cf["id"].(string), map[string]any{"tls_mode": "auto"}); m["tls_mode"] != "auto" || px.applied != modeBase+1 {
		t.Fatalf("switch to auto = %v, applied %d", m, px.applied-modeBase)
	}
	e.must(200, "PATCH", "/api/domains/"+cf["id"].(string), map[string]any{"tls_mode": "auto"}) // no change, no reload
	if px.applied != modeBase+1 {
		t.Fatal("unchanged mode reloaded the proxy")
	}
	e.must(204, "DELETE", "/api/domains/"+cf["id"].(string), nil)

	// The panel's domain and app domains exclude each other, both ways.
	e.must(409, "PUT", "/api/settings/panel", map[string]any{"domain": "shop.example.com"})
	e.must(200, "PUT", "/api/settings/panel", map[string]any{"domain": "panel.example.com"})
	e.must(409, "POST", domains, map[string]any{"hostname": "panel.example.com"})

	if got := e.list(domains); len(got) != 1 || got[0]["id"] != d["id"] {
		t.Fatalf("list = %v", got)
	}

	// DNS hint.
	dns := "/api/domains/" + d["id"].(string) + "/dns"
	e.srv.lookupHost = func(_ context.Context, host string) ([]string, error) {
		if host != "shop.example.com" {
			t.Errorf("looked up %q", host)
		}
		return []string{"203.0.113.5"}, nil
	}
	e.srv.serverAddrs = func() []string { return []string{"203.0.113.5"} }
	if m := e.must(200, "GET", dns, nil); m["resolves"] != true || m["points_here"] != true {
		t.Errorf("dns match = %v", m)
	}
	e.srv.serverAddrs = func() []string { return []string{"198.51.100.1"} }
	if m := e.must(200, "GET", dns, nil); m["resolves"] != true || m["points_here"] != false {
		t.Errorf("dns mismatch = %v", m)
	}
	e.srv.serverAddrs = func() []string { return []string{} } // behind NAT: cannot tell
	if m := e.must(200, "GET", dns, nil); m["points_here"] != nil {
		t.Errorf("dns unknown = %v", m)
	}
	e.srv.lookupHost = func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") }
	if m := e.must(200, "GET", dns, nil); m["resolves"] != false || len(m["addresses"].([]any)) != 0 {
		t.Errorf("dns unresolved = %v", m)
	}
	e.must(404, "GET", "/api/domains/missing/dns", nil)

	// Proxy down: saved, and the caller is told it is not live.
	px.err = errors.New("down")
	e.must(503, "POST", domains, map[string]any{"hostname": "www.shop.example.com"})
	if got := e.list(domains); len(got) != 2 {
		t.Fatalf("domain not saved while the proxy was down: %v", got)
	}
	px.err = nil

	before := px.applied
	if code, _ := e.raw("DELETE", "/api/domains/"+d["id"].(string), nil); code != 204 || px.applied != before+1 {
		t.Fatalf("delete = %d, applied %d -> %d", code, before, px.applied)
	}
	e.must(404, "DELETE", "/api/domains/"+d["id"].(string), nil)

	// Deleting the app frees its hostnames.
	if code, _ := e.raw("DELETE", "/api/apps/"+id, nil); code != 204 {
		t.Fatal("delete app")
	}
	e.must(201, "POST", "/api/apps/"+other+"/domains", map[string]any{"hostname": "www.shop.example.com"})
}

func TestGitAppsAPI(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "Shop"})["id"].(string)
	apps := "/api/projects/" + pid + "/apps"
	git := func(over map[string]any) map[string]any {
		m := map[string]any{"name": "api", "source_type": "git", "git_repository": "https://github.com/org/api.git", "port": 8080}
		for k, v := range over {
			m[k] = v
		}
		return m
	}

	for _, bad := range []map[string]any{
		git(map[string]any{"source_type": "svn"}),
		git(map[string]any{"git_repository": ""}),
		git(map[string]any{"git_repository": "http://github.com/org/api.git"}),
		git(map[string]any{"git_repository": "git@github.com:org/api.git"}),
		git(map[string]any{"git_repository": "file:///etc"}),
		git(map[string]any{"git_repository": "https://user:pw@github.com/org/api.git"}),
		git(map[string]any{"git_repository": "https://github.com/"}),
		git(map[string]any{"git_repository": "https://github.com/org/api --upload-pack=x"}),
		git(map[string]any{"git_branch": "-x"}),
		git(map[string]any{"git_branch": "a..b"}),
		git(map[string]any{"git_branch": "feature/"}),
		git(map[string]any{"builder": "railpack"}),
		git(map[string]any{"build_context": "../x"}),
		git(map[string]any{"build_context": "/etc"}),
		git(map[string]any{"dockerfile_path": "../Dockerfile"}),
		git(map[string]any{"dockerfile_path": "."}),
	} {
		if code, m := e.call("POST", apps, bad); code != 422 {
			t.Errorf("POST %v = %d %v, want 422", bad, code, m)
		}
	}

	a := e.must(201, "POST", apps, git(map[string]any{"build_context": "./services/api/", "image": "ignored:1"}))
	want := map[string]any{
		"source_type": "git", "image": "", "git_repository": "https://github.com/org/api.git", "git_branch": "main",
		"builder": "dockerfile", "build_context": "services/api", "dockerfile_path": "Dockerfile",
	}
	for k, v := range want {
		if a[k] != v {
			t.Errorf("created %s = %v, want %v", k, a[k], v)
		}
	}
	id := a["id"].(string)

	// Partial update keeps the rest; "." is the repository root.
	a = e.must(200, "PATCH", "/api/apps/"+id, map[string]any{"git_branch": "release/2.0", "build_context": "."})
	if a["git_branch"] != "release/2.0" || a["build_context"] != "" || a["git_repository"] != want["git_repository"] {
		t.Errorf("after update = %v", a)
	}

	// Switching to an image needs an image, and clears the Git settings.
	e.must(422, "PATCH", "/api/apps/"+id, map[string]any{"source_type": "image"})
	a = e.must(200, "PATCH", "/api/apps/"+id, map[string]any{"source_type": "image", "image": "nginx:1.27"})
	if a["source_type"] != "image" || a["git_repository"] != "" || a["builder"] != "" {
		t.Errorf("after switch to image = %v", a)
	}
}
