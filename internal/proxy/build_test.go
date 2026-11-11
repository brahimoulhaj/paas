package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func build(t *testing.T, c Config) map[string]any {
	t.Helper()
	raw, err := Build(c)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// dig walks nested objects and arrays: dig(v, "apps", "http", 0, "x").
func dig(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("dig %v: not an object at %q", path, k)
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok || k >= len(a) {
				t.Fatalf("dig %v: no index %d", path, k)
			}
			v = a[k]
		}
	}
	return v
}

func TestBuildFreshInstall(t *testing.T) {
	cfg := build(t, Config{AdminListen: "unix//run/caddy/admin.sock", PanelUpstream: "berth:8080"})

	if got := dig(t, cfg, "admin", "listen"); got != "unix//run/caddy/admin.sock" {
		t.Errorf("admin listen = %v", got)
	}
	servers := dig(t, cfg, "apps", "http", "servers").(map[string]any)
	if _, has := servers["https"]; has {
		t.Error("an https server exists without any hostname")
	}
	routes := dig(t, servers, "http", "routes").([]any)
	if len(routes) != 1 {
		t.Fatalf("http routes = %d, want only the panel catch-all", len(routes))
	}
	if _, has := routes[0].(map[string]any)["match"]; has {
		t.Error("the panel catch-all has a matcher")
	}
	if got := dig(t, routes[0], "handle", 0, "upstreams", 0, "dial"); got != "berth:8080" {
		t.Errorf("catch-all upstream = %v", got)
	}
	if _, has := dig(t, cfg, "apps").(map[string]any)["tls"]; has {
		t.Error("tls app configured with nothing to configure")
	}
}

func TestBuildRoutes(t *testing.T) {
	cfg := build(t, Config{
		AdminListen:   ":2019",
		PanelUpstream: "berth:8080",
		PanelDomain:   "panel.example.com",
		ACMEEmail:     "ops@example.com",
		Routes: []Route{
			{ID: "b", Hosts: []string{"b.example.com", "www.b.example.com"}, Upstreams: []string{"app-b:3000"}},
			{ID: "a", Hosts: []string{"a.test"}}, // stopped app: no upstreams
		},
	})

	https := dig(t, cfg, "apps", "http", "servers", "https", "routes").([]any)
	var ids []string
	for _, r := range https {
		ids = append(ids, r.(map[string]any)["@id"].(string))
	}
	if strings.Join(ids, ",") != "route-panel,route-a,route-b" {
		t.Errorf("https route order = %v", ids)
	}
	if got := dig(t, https[0], "handle", 0, "upstreams", 0, "dial"); got != "berth:8080" {
		t.Errorf("panel route upstream = %v", got)
	}
	if ups := dig(t, https[1], "handle", 0, "upstreams").([]any); len(ups) != 0 {
		t.Errorf("stopped app upstreams = %v, want an empty list (not null, so PATCH works)", ups)
	}
	if hosts := dig(t, https[2], "match", 0, "host").([]any); len(hosts) != 2 {
		t.Errorf("route b hosts = %v", hosts)
	}
	if got := dig(t, cfg, "apps", "http", "servers", "https", "automatic_https", "disable_redirects"); got != true {
		t.Error("automatic redirects not disabled")
	}

	// HTTP: one redirect per route, then the panel catch-all last.
	http := dig(t, cfg, "apps", "http", "servers", "http", "routes").([]any)
	if len(http) != 4 {
		t.Fatalf("http routes = %d, want 3 redirects + catch-all", len(http))
	}
	if got := dig(t, http[0], "handle", 0, "status_code"); got != float64(308) {
		t.Errorf("redirect status = %v", got)
	}
	if _, has := http[3].(map[string]any)["match"]; has {
		t.Error("catch-all is not last")
	}

	// TLS: internal names get the internal issuer, the rest ACME with the email.
	policies := dig(t, cfg, "apps", "tls", "automation", "policies").([]any)
	if len(policies) != 2 {
		t.Fatalf("tls policies = %v", policies)
	}
	if subj := dig(t, policies[0], "subjects").([]any); len(subj) != 1 || subj[0] != "a.test" {
		t.Errorf("internal subjects = %v", subj)
	}
	if got := dig(t, policies[0], "issuers", 0, "module"); got != "internal" {
		t.Errorf("internal issuer = %v", got)
	}
	if got := dig(t, policies[1], "issuers", 0, "email"); got != "ops@example.com" {
		t.Errorf("acme email = %v", got)
	}
}

func TestBuildRejects(t *testing.T) {
	base := Config{AdminListen: ":2019", PanelUpstream: "berth:8080"}
	for name, mutate := range map[string]func(*Config){
		"no admin":      func(c *Config) { c.AdminListen = "" },
		"bad upstream":  func(c *Config) { c.PanelUpstream = "berth" },
		"bad host":      func(c *Config) { c.Routes = []Route{{ID: "a", Hosts: []string{"Not A Host"}}} },
		"no hosts":      func(c *Config) { c.Routes = []Route{{ID: "a"}} },
		"empty id":      func(c *Config) { c.Routes = []Route{{Hosts: []string{"a.example.com"}}} },
		"id with slash": func(c *Config) { c.Routes = []Route{{ID: "a/b", Hosts: []string{"a.example.com"}}} },
		"duplicate id": func(c *Config) {
			c.Routes = []Route{{ID: "a", Hosts: []string{"a.example.com"}}, {ID: "a", Hosts: []string{"b.example.com"}}}
		},
		"duplicate host": func(c *Config) {
			c.Routes = []Route{{ID: "a", Hosts: []string{"a.example.com"}}, {ID: "b", Hosts: []string{"a.example.com"}}}
		},
		"host is panel's": func(c *Config) {
			c.PanelDomain = "a.example.com"
			c.Routes = []Route{{ID: "a", Hosts: []string{"a.example.com"}}}
		},
		"route upstream": func(c *Config) {
			c.Routes = []Route{{ID: "a", Hosts: []string{"a.example.com"}, Upstreams: []string{"nope"}}}
		},
		"panel id reused": func(c *Config) {
			c.PanelDomain = "p.example.com"
			c.Routes = []Route{{ID: "panel", Hosts: []string{"a.example.com"}}}
		},
	} {
		c := base
		mutate(&c)
		if _, err := Build(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidHost(t *testing.T) {
	for _, ok := range []string{"example.com", "a.b-c.example.com", "localhost", "app.test", "xn--bcher-kva.example"} {
		if err := ValidHost(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "example", "Example.com", "*.example.com", "a..com", "-a.com", "a-.com", "a.com:443", "10.0.0.1", "::1", "a b.com", "https://a.com", strings.Repeat("a", 64) + ".com"} {
		if err := ValidHost(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
