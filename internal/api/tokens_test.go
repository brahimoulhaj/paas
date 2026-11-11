package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"panel/internal/brand"
)

// bearer sends a request with an API token and no cookies.
func (e *env) bearer(token, method, path string, body any) (int, []byte) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.url+path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func (e *env) listTokens() []map[string]any {
	e.t.Helper()
	res, err := e.client.Get(e.url + "/api/tokens")
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		e.t.Fatalf("list tokens = %d", res.StatusCode)
	}
	var out []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestAPITokens(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.call("POST", "/api/tokens", map[string]any{"name": "ci"}); code != 401 {
		t.Fatalf("create signed out = %d", code)
	}
	e.signIn()

	for _, bad := range []map[string]any{{"name": ""}, {"name": "   "}, {"name": strings.Repeat("x", 65)}} {
		if code, m := e.call("POST", "/api/tokens", bad); code != 422 {
			t.Errorf("create %v = %d %v, want 422", bad, code, m)
		}
	}

	code, m := e.call("POST", "/api/tokens", map[string]any{"name": " ci "})
	if code != 201 || m["name"] != "ci" || m["last_used_at"] != nil {
		t.Fatalf("create = %d %v", code, m)
	}
	secret, _ := m["token"].(string)
	if !strings.HasPrefix(secret, brand.Slug+"_") || len(secret) < 40 {
		t.Fatalf("token = %q", secret)
	}
	tokenID := m["id"].(string)

	list := e.listTokens()
	if len(list) != 1 || list[0]["id"] != tokenID {
		t.Fatalf("list = %v", list)
	}
	if _, has := list[0]["token"]; has {
		t.Fatal("list leaks the secret")
	}

	// The token authenticates API calls without a cookie and records its use.
	if code, body := e.bearer(secret, "GET", "/api/auth/me", nil); code != 200 || !strings.Contains(string(body), "a@b.co") {
		t.Fatalf("me with token = %d %s", code, body)
	}
	if code, body := e.bearer(secret, "POST", "/api/projects", map[string]any{"name": "from-ci"}); code != 201 {
		t.Fatalf("create project with token = %d %s", code, body)
	}
	if e.listTokens()[0]["last_used_at"] == nil {
		t.Fatal("last_used_at not recorded")
	}
	events := e.eventActors()
	if !strings.Contains(events, "a@b.co (token ci)") {
		t.Fatalf("event actors = %s", events)
	}

	// Tokens cannot manage tokens.
	for _, c := range []struct{ method, path string }{{"GET", "/api/tokens"}, {"POST", "/api/tokens"}, {"DELETE", "/api/tokens/" + tokenID}} {
		if code, body := e.bearer(secret, c.method, c.path, map[string]any{"name": "x"}); code != 403 {
			t.Errorf("%s %s with token = %d %s, want 403", c.method, c.path, code, body)
		}
	}

	// Bad credentials are refused, not treated as signed out-and-fall-back-to-cookie.
	for _, h := range []string{"Bearer nope", "Basic abc", "Bearer ", "token " + secret} {
		req, _ := http.NewRequest("GET", e.url+"/api/auth/me", nil)
		req.Header.Set("Authorization", h)
		res, err := e.client.Do(req) // carries the session cookie
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Errorf("Authorization %q = %d, want 401", h, res.StatusCode)
		}
	}

	if code, _ := e.call("DELETE", "/api/tokens/01NOSUCHTOKEN0000000000000", nil); code != 404 {
		t.Fatalf("revoke unknown = %d", code)
	}
	if code, _ := e.call("DELETE", "/api/tokens/"+tokenID, nil); code != 204 {
		t.Fatalf("revoke = %d", code)
	}
	if code, _ := e.bearer(secret, "GET", "/api/auth/me", nil); code != 401 {
		t.Fatalf("revoked token still works: %d", code)
	}
	if len(e.listTokens()) != 0 {
		t.Fatal("token still listed after revoke")
	}
}

// eventActors joins the actor of every recorded event.
func (e *env) eventActors() string {
	e.t.Helper()
	res, err := e.client.Get(e.url + "/api/events")
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out []map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	var actors []string
	for _, ev := range out {
		a, _ := ev["actor"].(string)
		actors = append(actors, a)
	}
	return strings.Join(actors, "|")
}
