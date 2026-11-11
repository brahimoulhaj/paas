package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Admin is a client for Caddy's admin API.
type Admin struct {
	http *http.Client
	base string
}

// NewAdmin takes "unix:/path/to/admin.sock" or "tcp:host:port".
func NewAdmin(addr string) (*Admin, error) {
	kind, rest, ok := strings.Cut(addr, ":")
	if !ok || rest == "" {
		return nil, fmt.Errorf("proxy admin: invalid address %q", addr)
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second}
	a := &Admin{http: &http.Client{Transport: tr, Timeout: 30 * time.Second}}
	switch kind {
	case "unix":
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", rest)
		}
		a.base = "http://caddy" // the host is ignored on a unix socket
	case "tcp":
		tr.DialContext = dialer.DialContext
		a.base = "http://" + rest
	default:
		return nil, fmt.Errorf("proxy admin: invalid address %q", addr)
	}
	return a, nil
}

// Ping reports whether the admin API answers.
func (a *Admin) Ping(ctx context.Context) error {
	return a.do(ctx, http.MethodGet, "/config/admin", nil)
}

// Load replaces the whole running configuration. Caddy applies it without dropping
// connections and keeps the old one if the new one fails to load.
func (a *Admin) Load(ctx context.Context, config []byte) error {
	return a.do(ctx, http.MethodPost, "/load", config)
}

// SetUpstreams replaces the upstreams of one route in a single atomic change, leaving
// every other route untouched. This is the switch at the heart of a zero-downtime deploy.
func (a *Admin) SetUpstreams(ctx context.Context, routeID string, upstreams []string) error {
	list := make([]object, 0, len(upstreams))
	for _, u := range upstreams {
		if err := validUpstream(u); err != nil {
			return err
		}
		list = append(list, object{"dial": u})
	}
	body, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return a.do(ctx, http.MethodPatch, "/id/"+url.PathEscape(routeKey(routeID))+"/handle/0/upstreams", body)
}

func (a *Admin) do(ctx context.Context, method, path string, body []byte) error {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("caddy admin %s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("caddy admin %s %s: %s: %s", method, path, res.Status, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, res.Body)
	return nil
}
