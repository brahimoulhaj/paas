// Package proxy runs Caddy in front of the panel and every app. The panel owns the whole
// Caddy configuration: it is rebuilt from the database and pushed through the admin API,
// never edited in place, so the database stays the single source of truth.
package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
)

// Config is everything the Caddy configuration is derived from.
type Config struct {
	AdminListen   string  // Caddy network address of the admin API, e.g. "unix//run/caddy/admin.sock"
	PanelUpstream string  // host:port where Caddy reaches the panel
	PanelDomain   string  // optional; served over HTTPS with an automatic certificate
	ACMEEmail     string  // optional; the account email for the certificate authority
	Routes        []Route // one per app (or built-in service) that has domains
}

// Route sends requests for a set of hostnames to a set of upstreams over HTTPS with an
// automatic certificate; plain HTTP requests for those hostnames are redirected.
type Route struct {
	ID        string   // stable id, e.g. the app id; addresses the route in the admin API
	Hosts     []string // hostnames, lowercase
	Upstreams []string // host:port on the shared network; empty answers 503
}

// Ports Caddy listens on inside its container. The host ports they are published on are
// a property of the container, not of this configuration.
const (
	httpListen  = ":80"
	httpsListen = ":443"
)

type object = map[string]any

func routeKey(id string) string { return "route-" + id }

// Build returns the complete Caddy JSON configuration for c.
//
// Layout: an "https" server holds one route per hostname group; an "http" server redirects
// those hostnames to HTTPS and sends everything else to the panel, which is what makes
// http://<server-ip> work on a fresh install. Certificates come from Caddy's automatic
// HTTPS, driven by the host matchers on the "https" server.
func Build(c Config) ([]byte, error) {
	if c.AdminListen == "" {
		return nil, errors.New("proxy config: admin listen address is required")
	}
	if err := validUpstream(c.PanelUpstream); err != nil {
		return nil, fmt.Errorf("proxy config: panel upstream: %w", err)
	}

	routes := append([]Route(nil), c.Routes...)
	sort.Slice(routes, func(i, j int) bool { return routes[i].ID < routes[j].ID })
	if c.PanelDomain != "" {
		// The panel is a route like any other; listed first so its id is predictable.
		routes = append([]Route{{ID: "panel", Hosts: []string{c.PanelDomain}, Upstreams: []string{c.PanelUpstream}}}, routes...)
	}

	seenID := map[string]bool{}
	seenHost := map[string]string{}
	var httpsRoutes, httpRoutes []object
	var allHosts []string
	for _, r := range routes {
		if r.ID == "" || strings.ContainsAny(r.ID, "/ \t\n") {
			return nil, fmt.Errorf("proxy config: invalid route id %q", r.ID)
		}
		if seenID[r.ID] {
			return nil, fmt.Errorf("proxy config: duplicate route id %q", r.ID)
		}
		seenID[r.ID] = true
		if len(r.Hosts) == 0 {
			return nil, fmt.Errorf("proxy config: route %s has no hostnames", r.ID)
		}
		for _, h := range r.Hosts {
			if err := ValidHost(h); err != nil {
				return nil, fmt.Errorf("proxy config: route %s: %w", r.ID, err)
			}
			if other, dup := seenHost[h]; dup {
				return nil, fmt.Errorf("proxy config: hostname %s is used by routes %s and %s", h, other, r.ID)
			}
			seenHost[h] = r.ID
		}
		upstreams := make([]object, 0, len(r.Upstreams))
		for _, u := range r.Upstreams {
			if err := validUpstream(u); err != nil {
				return nil, fmt.Errorf("proxy config: route %s: %w", r.ID, err)
			}
			upstreams = append(upstreams, object{"dial": u})
		}
		allHosts = append(allHosts, r.Hosts...)
		httpsRoutes = append(httpsRoutes, object{
			"@id":      routeKey(r.ID),
			"match":    []object{{"host": r.Hosts}},
			"handle":   []object{{"handler": "reverse_proxy", "upstreams": upstreams}},
			"terminal": true,
		})
		httpRoutes = append(httpRoutes, object{
			"match": []object{{"host": r.Hosts}},
			"handle": []object{{
				"handler":     "static_response",
				"status_code": 308,
				"headers":     object{"Location": []string{"https://{http.request.host}{http.request.uri}"}},
			}},
			"terminal": true,
		})
	}

	// Last on the HTTP server: anything that is not a known hostname reaches the panel.
	httpRoutes = append(httpRoutes, object{
		"handle": []object{{"handler": "reverse_proxy", "upstreams": []object{{"dial": c.PanelUpstream}}}},
	})

	servers := object{
		"http": object{"listen": []string{httpListen}, "routes": httpRoutes},
	}
	if len(httpsRoutes) > 0 {
		servers["https"] = object{
			"listen": []string{httpsListen},
			"routes": httpsRoutes,
			// The redirects are written out above, ahead of the panel catch-all.
			"automatic_https": object{"disable_redirects": true},
		}
	}

	apps := object{"http": object{"servers": servers}}
	if policies := tlsPolicies(allHosts, c.ACMEEmail); len(policies) > 0 {
		apps["tls"] = object{"automation": object{"policies": policies}}
	}

	return json.MarshalIndent(object{
		// persist=false: the panel writes the boot configuration itself (see Manager).
		"admin": object{"listen": c.AdminListen, "config": object{"persist": false}},
		"apps":  apps,
	}, "", "  ")
}

// tlsPolicies gives names no public authority will sign (localhost, .test, …) a certificate
// from Caddy's internal authority, and sets the account email for everything else.
func tlsPolicies(hosts []string, email string) []object {
	var internal []string
	for _, h := range hosts {
		if isInternalName(h) {
			internal = append(internal, h)
		}
	}
	var policies []object
	if len(internal) > 0 {
		policies = append(policies, object{"subjects": internal, "issuers": []object{{"module": "internal"}}})
	}
	if email != "" {
		policies = append(policies, object{"issuers": []object{{"module": "acme", "email": email}}})
	}
	return policies
}

func isInternalName(h string) bool {
	if h == "localhost" {
		return true
	}
	for _, suffix := range []string{".localhost", ".test", ".internal", ".local", ".example", ".invalid"} {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

// ValidHost reports whether h is a hostname the proxy can route: a lowercase DNS name
// with at least two labels (or "localhost"), no wildcard, no port, not an IP address.
func ValidHost(h string) error {
	if h == "" || len(h) > 253 {
		return fmt.Errorf("invalid hostname %q", h)
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return fmt.Errorf("invalid hostname %q: an IP address cannot get a certificate", h)
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 && h != "localhost" {
		return fmt.Errorf("invalid hostname %q", h)
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return fmt.Errorf("invalid hostname %q", h)
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return fmt.Errorf("invalid hostname %q", h)
			}
		}
	}
	return nil
}

func validUpstream(u string) error {
	host, port, err := net.SplitHostPort(u)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("invalid upstream %q, want host:port", u)
	}
	return nil
}
