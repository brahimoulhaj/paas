package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"panel/internal/account"
	"panel/internal/brand"
	"panel/internal/deploy"
	"panel/internal/executor/executortest"
	"panel/internal/store"
	"panel/internal/update"
)

type env struct {
	t        *testing.T
	url      string
	client   *http.Client
	accounts *account.Service
	srv      *Server
	ex       *executortest.Fake
	deployer *deploy.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	acc := account.New(st)
	web := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("js")},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := New(st, acc, log, "test", web)
	ex := executortest.New()
	deployer := deploy.New(context.Background(), deploy.Options{
		Store: st, Executor: ex, DataDir: dir, Log: log, StartGrace: 10 * time.Millisecond,
		// New apps get a TCP health check: every fake container accepts the connection.
		Dial: func(context.Context, string, string) (net.Conn, error) {
			c1, c2 := net.Pipe()
			c2.Close()
			return c1, nil
		},
	})
	api.SetDeployer(deployer)
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &env{t: t, url: srv.URL, client: &http.Client{Jar: jar}, accounts: acc, srv: api, ex: ex, deployer: deployer}
}

// call sends a request and decodes the JSON response into a generic map.
func (e *env) call(method, path string, body any, headers ...string) (int, map[string]any) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.url+path, r)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestSetupLoginFlow(t *testing.T) {
	e := newEnv(t)
	creds := map[string]any{"email": "Owner@Example.com", "password": "s3cret-pass"}

	if code, m := e.call("GET", "/api/meta", nil); code != 200 || m["setup_required"] != true || m["name"] != brand.Name {
		t.Fatalf("meta = %d %v", code, m)
	}
	if code, _ := e.call("GET", "/api/auth/me", nil); code != 401 {
		t.Fatalf("me before login = %d", code)
	}
	if code, _ := e.call("POST", "/api/setup", map[string]any{"token": "nope", "email": "a@b.co", "password": "s3cret-pass"}); code != 403 {
		t.Fatalf("setup with bad token = %d", code)
	}

	token, _, err := e.accounts.IssueSetupToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if code, m := e.call("POST", "/api/setup", map[string]any{"token": token, "email": "bad", "password": "s3cret-pass"}); code != 422 {
		t.Fatalf("setup with bad email = %d %v", code, m)
	}
	if code, m := e.call("POST", "/api/setup", map[string]any{"token": token, "email": "a@b.co", "password": "short"}); code != 422 {
		t.Fatalf("setup with short password = %d %v", code, m)
	}
	code, m := e.call("POST", "/api/setup", map[string]any{"token": token, "email": creds["email"], "password": creds["password"]})
	if code != 201 || m["email"] != "owner@example.com" || m["role"] != "owner" {
		t.Fatalf("setup = %d %v", code, m)
	}
	if _, has := m["password_hash"]; has {
		t.Fatal("password hash leaked")
	}

	if code, m := e.call("GET", "/api/meta", nil); code != 200 || m["setup_required"] != false {
		t.Fatalf("meta after setup = %d %v", code, m)
	}
	if code, m := e.call("GET", "/api/auth/me", nil); code != 200 || m["email"] != "owner@example.com" {
		t.Fatalf("me after setup = %d %v", code, m)
	}
	// The token is single use, and setup is closed once an owner exists.
	if code, _ := e.call("POST", "/api/setup", map[string]any{"token": token, "email": "x@y.co", "password": "s3cret-pass"}); code != 409 {
		t.Fatalf("second setup = %d", code)
	}

	if code, _ := e.call("POST", "/api/auth/logout", nil); code != 204 {
		t.Fatalf("logout = %d", code)
	}
	if code, _ := e.call("GET", "/api/auth/me", nil); code != 401 {
		t.Fatalf("me after logout = %d", code)
	}
	if code, _ := e.call("POST", "/api/auth/login", map[string]any{"email": creds["email"], "password": "wrong-pass"}); code != 401 {
		t.Fatalf("login wrong password = %d", code)
	}
	if code, _ := e.call("POST", "/api/auth/login", map[string]any{"email": "nobody@example.com", "password": "s3cret-pass"}); code != 401 {
		t.Fatalf("login unknown email = %d", code)
	}
	if code, _ := e.call("POST", "/api/auth/login", creds); code != 200 {
		t.Fatalf("login = %d", code)
	}
	if code, _ := e.call("GET", "/api/auth/me", nil); code != 200 {
		t.Fatalf("me after login = %d", code)
	}

	// A password reset from the CLI signs every session out.
	if _, err := e.accounts.ResetPassword(context.Background(), "", "new-s3cret-pass"); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.call("GET", "/api/auth/me", nil); code != 401 {
		t.Fatalf("me after reset = %d", code)
	}
}

func TestCrossOriginWritesBlocked(t *testing.T) {
	e := newEnv(t)
	body := map[string]any{"email": "a@b.co", "password": "s3cret-pass"}
	if code, m := e.call("POST", "/api/auth/login", body, "Origin", "https://evil.example"); code != 403 {
		t.Fatalf("foreign Origin = %d %v", code, m)
	}
	if code, _ := e.call("POST", "/api/auth/login", body, "Sec-Fetch-Site", "cross-site"); code != 403 {
		t.Fatalf("cross-site fetch = %d", code)
	}
	if code, _ := e.call("POST", "/api/auth/login", body, "Origin", e.url, "Sec-Fetch-Site", "same-origin"); code != 401 {
		t.Fatalf("same-origin write = %d, want it to reach the handler", code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	body := map[string]any{"email": "a@b.co", "password": "wrong-pass"}
	for i := range 10 {
		if code, _ := e.call("POST", "/api/auth/login", body); code != 401 {
			t.Fatalf("attempt %d = %d", i+1, code)
		}
	}
	if code, _ := e.call("POST", "/api/auth/login", body); code != 429 {
		t.Fatalf("attempt 11 = %d, want 429", code)
	}
}

func TestBadRequests(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("POST", e.url+"/api/auth/login", bytes.NewReader([]byte("{not json")))
	res, _ := e.client.Do(req)
	if res.StatusCode != 400 {
		t.Fatalf("malformed body = %d", res.StatusCode)
	}
	if code, _ := e.call("POST", "/api/auth/login", map[string]any{"email": "a@b.co", "password": "x", "extra": 1}); code != 400 {
		t.Fatalf("unknown field = %d", code)
	}
	if code, m := e.call("GET", "/api/nope", nil); code != 404 || m["error"] == nil {
		t.Fatalf("unknown endpoint = %d %v", code, m)
	}
	if code, _ := e.call("GET", "/api/health", nil); code != 200 {
		t.Fatalf("health = %d", code)
	}
}

func TestSPA(t *testing.T) {
	e := newEnv(t)
	get := func(path string) (int, string, http.Header) {
		res, err := e.client.Get(e.url + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res.Header
	}
	for _, p := range []string{"/", "/login", "/projects/abc/apps"} { // client-side routes
		if code, body, _ := get(p); code != 200 || body != "<html>app</html>" {
			t.Fatalf("GET %s = %d %q", p, code, body)
		}
	}
	if code, body, h := get("/assets/app.js"); code != 200 || body != "js" || h.Get("Cache-Control") == "" {
		t.Fatalf("asset = %d %q %v", code, body, h)
	}
	if code, _, _ := get("/assets/missing.js"); code != 404 {
		t.Fatalf("missing asset = %d", code)
	}
}

type fakeProxy struct {
	applied int
	err     error
}

func (p *fakeProxy) Apply(context.Context) error { p.applied++; return p.err }

func (e *env) signIn() {
	e.t.Helper()
	token, _, err := e.accounts.IssueSetupToken(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	if code, m := e.call("POST", "/api/setup", map[string]any{"token": token, "email": "a@b.co", "password": "s3cret-pass"}); code != 201 {
		e.t.Fatalf("setup = %d %v", code, m)
	}
}

func TestPanelSettings(t *testing.T) {
	e := newEnv(t)
	px := &fakeProxy{}
	e.srv.SetProxy(px)

	if code, _ := e.call("GET", "/api/settings/panel", nil); code != 401 {
		t.Fatalf("signed out GET = %d", code)
	}
	if code, _ := e.call("PUT", "/api/settings/panel", map[string]any{"domain": "panel.example.com"}); code != 401 {
		t.Fatalf("signed out PUT = %d", code)
	}
	e.signIn()

	if code, m := e.call("GET", "/api/settings/panel", nil); code != 200 || m["domain"] != "" {
		t.Fatalf("initial = %d %v", code, m)
	}
	for _, bad := range []map[string]any{
		{"domain": "not a host"},
		{"domain": "10.0.0.1"},
		{"domain": "https://panel.example.com"},
		{"domain": "panel.example.com", "acme_email": "nope"},
	} {
		if code, m := e.call("PUT", "/api/settings/panel", bad); code != 422 {
			t.Errorf("PUT %v = %d %v, want 422", bad, code, m)
		}
	}
	if px.applied != 0 {
		t.Fatalf("invalid input reached the proxy %d times", px.applied)
	}

	code, m := e.call("PUT", "/api/settings/panel", map[string]any{"domain": " Panel.Example.com ", "acme_email": "ops@example.com"})
	if code != 200 || m["domain"] != "panel.example.com" || m["acme_email"] != "ops@example.com" || px.applied != 1 {
		t.Fatalf("PUT = %d %v, applied %d", code, m, px.applied)
	}
	if code, m := e.call("GET", "/api/settings/panel", nil); code != 200 || m["domain"] != "panel.example.com" {
		t.Fatalf("after PUT = %d %v", code, m)
	}
	if code, m := e.call("GET", "/api/settings/panel", nil); m["tls_mode"] != "auto" {
		t.Fatalf("default tls_mode = %d %v", code, m)
	}
	if code, m := e.call("PUT", "/api/settings/panel", map[string]any{"domain": "panel.example.com", "tls_mode": "bogus"}); code != 422 {
		t.Fatalf("bad tls_mode = %d %v", code, m)
	}
	if code, m := e.call("PUT", "/api/settings/panel", map[string]any{"domain": "panel.example.com", "tls_mode": "http_only"}); code != 200 || m["tls_mode"] != "http_only" {
		t.Fatalf("http_only = %d %v", code, m)
	}
	if code, m := e.call("GET", "/api/settings/panel", nil); m["tls_mode"] != "http_only" {
		t.Fatalf("persisted tls_mode = %d %v", code, m)
	}
	if code, m := e.call("GET", "/api/settings/panel", nil); m["ip_access"] != true {
		t.Fatalf("default ip_access = %d %v", code, m)
	}
	if code, m := e.call("PUT", "/api/settings/panel", map[string]any{"domain": "", "ip_access": false}); code != 422 {
		t.Fatalf("ip access off without a domain = %d %v", code, m)
	}
	if code, m := e.call("PUT", "/api/settings/panel", map[string]any{"domain": "panel.example.com", "ip_access": false}); code != 200 || m["ip_access"] != false {
		t.Fatalf("ip access off = %d %v", code, m)
	}
	if code, m := e.call("GET", "/api/settings/panel", nil); m["ip_access"] != false {
		t.Fatalf("persisted ip_access = %d %v", code, m)
	}
	e.srv.lookupHost = func(_ context.Context, host string) ([]string, error) {
		if host != "panel.example.com" {
			t.Errorf("looked up %q", host)
		}
		return []string{"203.0.113.5"}, nil
	}
	if code, m := e.call("GET", "/api/settings/panel/dns", nil); code != 200 || m["resolves"] != true || m["hostname"] != "panel.example.com" {
		t.Fatalf("panel dns = %d %v", code, m)
	}

	// Proxy down: the setting is still saved, the caller is told it is not live yet.
	px.err = context.DeadlineExceeded
	if code, m := e.call("PUT", "/api/settings/panel", map[string]any{"domain": ""}); code != 503 {
		t.Fatalf("PUT with proxy down = %d %v", code, m)
	}
	if code, m := e.call("GET", "/api/settings/panel", nil); code != 200 || m["domain"] != "" {
		t.Fatalf("after failed apply = %d %v", code, m)
	}
	if code, _ := e.call("GET", "/api/settings/panel/dns", nil); code != 404 {
		t.Fatalf("panel dns without a domain = %d", code)
	}
}

func TestClientIP(t *testing.T) {
	for _, tc := range []struct{ remote, xff, want string }{
		{"203.0.113.9:4000", "", "203.0.113.9"},
		{"203.0.113.9:4000", "198.51.100.1", "203.0.113.9"}, // direct public peer: header ignored
		{"172.18.0.3:4000", "198.51.100.1", "198.51.100.1"}, // from the proxy
		{"127.0.0.1:4000", "10.0.0.1, 198.51.100.7", "198.51.100.7"},
		{"172.18.0.3:4000", "garbage", "172.18.0.3"},
		{"127.0.0.1:4000", "", "127.0.0.1"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if got := clientIP(r); got != tc.want {
			t.Errorf("clientIP(%s, xff=%q) = %s, want %s", tc.remote, tc.xff, got, tc.want)
		}
	}
}

func TestOnboarding(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.call("GET", "/api/onboarding", nil); code != 401 {
		t.Fatalf("signed out = %d", code)
	}
	e.signIn()
	if code, m := e.call("GET", "/api/onboarding", nil); code != 200 || m["completed"] != false {
		t.Fatalf("fresh = %d %v", code, m)
	}
	for range 2 { // idempotent
		if code, _ := e.call("POST", "/api/onboarding/complete", nil); code != 204 {
			t.Fatalf("complete = %d", code)
		}
	}
	if code, m := e.call("GET", "/api/onboarding", nil); code != 200 || m["completed"] != true {
		t.Fatalf("after complete = %d %v", code, m)
	}
}

type fakeUpdater struct {
	info    update.Info
	applied int
	err     error
}

func (f *fakeUpdater) Info() update.Info { return f.info }
func (f *fakeUpdater) Check(context.Context) update.Info {
	f.info.Available = true
	f.info.Latest = "0.2.0"
	return f.info
}
func (f *fakeUpdater) Apply(context.Context) error { f.applied++; return f.err }

func TestUpdates(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	// No updater (dev): readable, explains why.
	if code, m := e.call("GET", "/api/updates", nil); code != 200 || m["supported"] != false || m["reason"] == "" {
		t.Fatalf("no updater = %d %v", code, m)
	}
	if code, _ := e.call("POST", "/api/updates/apply", nil); code != 409 {
		t.Fatalf("apply without updater = %d", code)
	}

	fu := &fakeUpdater{info: update.Info{Supported: true, Version: "0.1.0", Image: "ghcr.io/me/berth:0.1.0", Status: "idle"}}
	e.srv.SetUpdater(fu)
	if code, m := e.call("POST", "/api/updates/check", nil); code != 200 || m["available"] != true || m["latest"] != "0.2.0" || m["auto"] != false {
		t.Fatalf("check = %d %v", code, m)
	}
	if code, m := e.call("PUT", "/api/updates/settings", map[string]any{"auto": true}); code != 200 || m["auto"] != true {
		t.Fatalf("auto on = %d %v", code, m)
	}
	if code, m := e.call("GET", "/api/updates", nil); code != 200 || m["auto"] != true {
		t.Fatalf("auto persisted = %d %v", code, m)
	}
	if code, _ := e.call("POST", "/api/updates/apply", nil); code != 202 || fu.applied != 1 {
		t.Fatalf("apply = %d, applied %d", code, fu.applied)
	}
	fu.err = update.ErrDeploying
	if code, _ := e.call("POST", "/api/updates/apply", nil); code != 409 {
		t.Fatalf("apply while deploying = %d", code)
	}
	fu.err = errors.New("pull failed")
	if code, _ := e.call("POST", "/api/updates/apply", nil); code != 502 {
		t.Fatalf("apply with failed pull = %d", code)
	}
}
