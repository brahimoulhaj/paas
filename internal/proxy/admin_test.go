package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recorded struct{ method, path, body, contentType string }

func recorder(got *[]recorded) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*got = append(*got, recorded{r.Method, r.URL.Path, string(b), r.Header.Get("Content-Type")})
		if strings.Contains(r.URL.Path, "missing") {
			http.Error(w, `{"error":"unknown object ID"}`, http.StatusNotFound)
		}
	})
}

func TestAdminTCP(t *testing.T) {
	var got []recorded
	srv := httptest.NewServer(recorder(&got))
	defer srv.Close()
	a, err := NewAdmin("tcp:" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := a.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Load(ctx, []byte(`{"apps":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := a.SetUpstreams(ctx, "app_1", []string{"app-1-new:3000"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetUpstreams(ctx, "app_1", nil); err != nil {
		t.Fatal(err)
	}
	want := []recorded{
		{"GET", "/config/admin", "", ""},
		{"POST", "/load", `{"apps":{}}`, "application/json"},
		{"PATCH", "/id/route-app_1/handle/0/upstreams", `[{"dial":"app-1-new:3000"}]`, "application/json"},
		{"PATCH", "/id/route-app_1/handle/0/upstreams", `[]`, "application/json"},
	}
	if len(got) != len(want) {
		t.Fatalf("requests = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Caddy's error body reaches the caller.
	err = a.SetUpstreams(ctx, "missing", []string{"x:1"})
	if err == nil || !strings.Contains(err.Error(), "unknown object ID") {
		t.Errorf("error = %v", err)
	}
	if err := a.SetUpstreams(ctx, "app_1", []string{"no-port"}); err == nil {
		t.Error("invalid upstream accepted")
	}
}

func TestAdminUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "pxy") // t.TempDir() is too long for a socket path on macOS
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "admin.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var got []recorded
	srv := &http.Server{Handler: recorder(&got)}
	go srv.Serve(ln)
	defer srv.Close()

	a, err := NewAdmin("unix:" + sock)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].path != "/config/admin" {
		t.Errorf("requests = %+v", got)
	}
}

func TestAdminBadAddress(t *testing.T) {
	for _, addr := range []string{"", "tcp", "unix:", "http://x"} {
		if _, err := NewAdmin(addr); err == nil {
			t.Errorf("%q accepted", addr)
		}
	}
}
