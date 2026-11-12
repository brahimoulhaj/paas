package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"panel/internal/executor"
)

func TestTerminalOriginAndDimensions(t *testing.T) {
	for _, tc := range []struct {
		origin string
		want   bool
	}{
		{"http://panel.test", true}, {"https://panel.test", true}, {"https://evil.test", false},
		{"", false}, {"null", false}, {"https://panel.test.evil.test", false}, {"https://user@panel.test", false},
	} {
		r := httptest.NewRequest("GET", "http://panel.test/api/apps/a/terminal", nil)
		r.Header.Set("Origin", tc.origin)
		if terminalOrigin(r) != tc.want {
			t.Errorf("origin %q", tc.origin)
		}
	}
	for _, size := range [][2]int{{0, 24}, {80, 0}, {501, 24}, {80, 301}, {-1, 24}} {
		if terminalSize(size[0], size[1]) {
			t.Errorf("accepted size %v", size)
		}
	}
	if !terminalSize(80, 24) {
		t.Fatal("normal size refused")
	}
}

func TestTerminalRequiresSession(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "/api/apps/a/terminal?cols=80&rows=24&shell=/bin/sh", nil)
	w := httptest.NewRecorder()
	s.requireAuth(sessionOnly(s.handleTerminal))(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated terminal = %d", w.Code)
	}
	w = httptest.NewRecorder()
	r.Header.Set("Origin", "https://evil.test")
	s.handleTerminal(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin terminal = %d", w.Code)
	}
}

type terminalStub struct {
	Deployer
	conn  net.Conn
	sizes chan [2]uint
}

func (d *terminalStub) OpenTerminal(context.Context, string, string, uint, uint) (executor.Terminal, error) {
	return &testPTY{Conn: d.conn, sizes: d.sizes}, nil
}

type testPTY struct {
	net.Conn
	sizes chan [2]uint
}

func (p *testPTY) Resize(_ context.Context, cols, rows uint) error {
	p.sizes <- [2]uint{cols, rows}
	return nil
}

func TestTerminalWebSocketBridge(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "terminal"})["id"].(string)
	a := e.must(201, "POST", "/api/projects/"+pid+"/apps", map[string]any{"name": "shell", "image": "busybox", "port": 80})
	server, process := net.Pipe()
	defer process.Close()
	defer server.Close()
	sizes := make(chan [2]uint, 1)
	e.srv.deployer = &terminalStub{Deployer: e.deployer, conn: server, sizes: sizes}
	dialer := websocket.Dialer{Jar: e.client.Jar}
	ws, _, err := dialer.Dial("ws"+strings.TrimPrefix(e.url, "http")+"/api/apps/"+a["id"].(string)+"/terminal?cols=80&rows=24&shell=/bin/sh", http.Header{"Origin": []string{e.url}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, ready, err := ws.ReadMessage()
	if err != nil || !strings.Contains(string(ready), "ready") {
		t.Fatalf("ready: %s %v", ready, err)
	}
	go process.Write([]byte("\x1b[32mhello\x1b[0m\r\n"))
	kind, output, err := ws.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || string(output) != "\x1b[32mhello\x1b[0m\r\n" {
		t.Fatalf("output: %q %v", output, err)
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte("pwd\r")); err != nil {
		t.Fatal(err)
	}
	process.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 32)
	n, err := process.Read(buf)
	if err != nil || string(buf[:n]) != "pwd\r" {
		t.Fatalf("input: %q %v", buf[:n], err)
	}
	ws.WriteJSON(map[string]any{"type": "resize", "cols": 120, "rows": 40})
	select {
	case size := <-sizes:
		if size != [2]uint{120, 40} {
			t.Fatal(size)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resize not delivered")
	}
	ws.Close()
	process.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := process.Read(buf); err == nil {
		t.Fatal("PTY not closed on disconnect")
	}
}
