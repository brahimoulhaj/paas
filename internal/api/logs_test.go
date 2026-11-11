package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type sseEvent struct {
	id, name string
	data     map[string]any
}

// stream opens an event stream and delivers its events; the channel closes when the
// server ends the response. cancel drops the connection, as a browser tab closing would.
func (e *env) stream(path, lastEventID string) (status int, events <-chan sseEvent, cancel func()) {
	e.t.Helper()
	ctx, cancelCtx := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", e.url+path, nil)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	res, err := e.client.Do(req)
	if err != nil {
		cancelCtx()
		e.t.Fatal(err)
	}
	ch := make(chan sseEvent, 64)
	if res.StatusCode != 200 {
		res.Body.Close()
		close(ch)
		return res.StatusCode, ch, cancelCtx
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		e.t.Errorf("content type = %q", ct)
	}
	go func() {
		defer close(ch)
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.name != "" {
					ch <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "id: "):
				ev.id = line[4:]
			case strings.HasPrefix(line, "event: "):
				ev.name = line[7:]
			case strings.HasPrefix(line, "data: "):
				_ = json.Unmarshal([]byte(line[6:]), &ev.data)
			}
		}
	}()
	e.t.Cleanup(cancelCtx)
	return 200, ch, cancelCtx
}

// next waits for one event; ok is false when the stream closed instead.
func next(t *testing.T, ch <-chan sseEvent) (sseEvent, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a stream event")
		return sseEvent{}, false
	}
}

func wantLog(t *testing.T, ch <-chan sseEvent, text string) sseEvent {
	t.Helper()
	ev, ok := next(t, ch)
	if !ok || ev.name != "log" || ev.data["text"] != text {
		t.Fatalf("event = %+v (open %v), want log %q", ev, ok, text)
	}
	return ev
}

// deployedApp signs in, creates a project and an app, deploys it and returns the app id
// and the container name.
func (e *env) deployedApp() (appID, container string) {
	e.t.Helper()
	e.signIn()
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "Shop"})["id"].(string)
	appID = e.must(201, "POST", "/api/projects/"+pid+"/apps", map[string]any{"name": "web", "image": "nginx:1", "port": 80})["id"].(string)
	dep := e.must(202, "POST", "/api/apps/"+appID+"/deploy", nil)["id"].(string)
	e.deployer.Wait()
	return appID, e.must(200, "GET", "/api/deployments/"+dep, nil)["container_name"].(string)
}

func TestAppLogStream(t *testing.T) {
	e := newEnv(t)
	if code, _, _ := e.stream("/api/apps/x/logs/stream", ""); code != 401 {
		t.Fatalf("signed out = %d", code)
	}
	e.signIn()
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "P"})["id"].(string)
	idle := e.must(201, "POST", "/api/projects/"+pid+"/apps", map[string]any{"name": "idle", "image": "nginx:1", "port": 80})["id"].(string)
	if code, _, _ := e.stream("/api/apps/"+idle+"/logs/stream", ""); code != 409 {
		t.Fatalf("never deployed = %d, want 409", code)
	}
	if code, _, _ := e.stream("/api/apps/missing/logs/stream", ""); code != 404 {
		t.Fatalf("unknown app = %d", code)
	}

	appID := e.must(201, "POST", "/api/projects/"+pid+"/apps", map[string]any{"name": "web", "image": "nginx:1", "port": 80})["id"].(string)
	dep := e.must(202, "POST", "/api/apps/"+appID+"/deploy", nil)["id"].(string)
	e.deployer.Wait()
	container := e.must(200, "GET", "/api/deployments/"+dep, nil)["container_name"].(string)
	path := "/api/apps/" + appID + "/logs/stream"

	for _, bad := range []string{"0", "abc", "999999"} {
		if code, _, _ := e.stream(path+"?tail="+bad, ""); code != 422 {
			t.Errorf("tail=%s = %d, want 422", bad, code)
		}
	}

	e.ex.AddLog(container, "stdout", "one")
	e.ex.AddLog(container, "stdout", "two")
	e.ex.AddLog(container, "stderr", "three")

	// Backlog limited by tail, then live lines as they are written.
	_, ch, _ := e.stream(path+"?tail=2", "")
	wantLog(t, ch, "two")
	three := wantLog(t, ch, "three")
	if three.data["stream"] != "stderr" || three.id == "" || three.data["time"] != three.id {
		t.Errorf("event = %+v, want stderr with its time as id", three)
	}
	e.ex.AddLog(container, "stdout", "four \"quoted\"\twith tab")
	wantLog(t, ch, "four \"quoted\"\twith tab")

	// A reconnect with Last-Event-ID gets only what came after that line.
	_, resumed, _ := e.stream(path, three.id)
	wantLog(t, resumed, "four \"quoted\"\twith tab")

	// The container stops: both streams say so and close.
	e.must(200, "POST", "/api/apps/"+appID+"/stop", nil)
	for name, c := range map[string]<-chan sseEvent{"first": ch, "resumed": resumed} {
		ev, ok := next(t, c)
		if !ok || ev.name != "end" || ev.data["reason"] != "the container stopped" {
			t.Errorf("%s stream: event = %+v (open %v), want end", name, ev, ok)
		}
		if _, ok := next(t, c); ok {
			t.Errorf("%s stream stayed open after end", name)
		}
	}
}

func TestLogStreamEndsOnShutdown(t *testing.T) {
	e := newEnv(t)
	appID, container := e.deployedApp()
	e.ex.AddLog(container, "stdout", "hello")
	_, ch, _ := e.stream("/api/apps/"+appID+"/logs/stream", "")
	wantLog(t, ch, "hello")
	e.srv.StopStreams()
	e.srv.StopStreams() // safe to call twice
	if ev, ok := next(t, ch); ok {
		t.Fatalf("stream delivered %+v after StopStreams, want it closed", ev)
	}
}

func TestDeploymentLogStream(t *testing.T) {
	e := newEnv(t)
	e.srv.logPoll = 5 * time.Millisecond
	if code, _, _ := e.stream("/api/deployments/x/log/stream", ""); code != 401 {
		t.Fatalf("signed out = %d", code)
	}
	e.signIn()
	if code, _, _ := e.stream("/api/deployments/missing/log/stream", ""); code != 404 {
		t.Fatalf("unknown deployment = %d", code)
	}
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "Shop"})["id"].(string)
	appID := e.must(201, "POST", "/api/projects/"+pid+"/apps", map[string]any{"name": "web", "image": "nginx:1", "port": 80})["id"].(string)

	// Follow a deployment while it runs: lines arrive before it finishes, then "end".
	e.ex.PullDelay = 150 * time.Millisecond
	dep := e.must(202, "POST", "/api/apps/"+appID+"/deploy", nil)["id"].(string)
	_, ch, _ := e.stream("/api/deployments/"+dep+"/log/stream", "")

	first, _ := next(t, ch)
	if first.name != "log" || !strings.HasPrefix(first.data["text"].(string), "Deploying web") || first.data["time"] == nil {
		t.Fatalf("first event = %+v", first)
	}
	if st := e.must(200, "GET", "/api/deployments/"+dep, nil)["status"]; st == "succeeded" {
		t.Error("the first line only arrived after the deployment finished")
	}
	var texts []string
	var ids []int64
	events := []sseEvent{first}
	for {
		ev, ok := next(t, ch)
		if !ok {
			t.Fatal("stream closed without an end event")
		}
		if ev.name == "end" {
			if ev.data["status"] != "succeeded" || ev.data["error"] != "" {
				t.Errorf("end = %+v", ev.data)
			}
			break
		}
		events = append(events, ev)
	}
	if _, ok := next(t, ch); ok {
		t.Error("stream stayed open after end")
	}
	for _, ev := range events {
		n, err := strconv.ParseInt(ev.id, 10, 64)
		if err != nil || (len(ids) > 0 && n <= ids[len(ids)-1]) {
			t.Fatalf("event ids are not increasing byte offsets: %q after %v", ev.id, ids)
		}
		ids = append(ids, n)
		texts = append(texts, ev.data["time"].(string)+" "+ev.data["text"].(string))
	}

	// The stream delivered exactly the file, last line included.
	_, raw := e.raw("GET", "/api/deployments/"+dep+"/log", nil)
	if got := strings.Join(texts, "\n") + "\n"; got != string(raw) {
		t.Errorf("stream:\n%s\nfile:\n%s", got, raw)
	}
	if last := texts[len(texts)-1]; !strings.HasSuffix(last, "Deployment succeeded") {
		t.Errorf("last streamed line = %q", last)
	}
	if ids[len(ids)-1] != int64(len(raw)) {
		t.Errorf("last id = %d, file is %d bytes", ids[len(ids)-1], len(raw))
	}

	// Resume from the middle: only the lines after that offset, then end.
	mid := len(events) / 2
	_, resumed, _ := e.stream("/api/deployments/"+dep+"/log/stream", events[mid-1].id)
	for _, want := range events[mid:] {
		ev, _ := next(t, resumed)
		if ev.name != "log" || ev.id != want.id || ev.data["text"] != want.data["text"] {
			t.Fatalf("resumed event = %+v, want %+v", ev, want)
		}
	}
	if ev, _ := next(t, resumed); ev.name != "end" {
		t.Errorf("after the resumed lines: %+v, want end", ev)
	}

	// A failed deployment ends with its error.
	e.ex.PullDelay = 0
	e.must(200, "PATCH", "/api/apps/"+appID, map[string]any{"image": "registry.example/missing:1"})
	failed := e.must(202, "POST", "/api/apps/"+appID+"/deploy", nil)["id"].(string)
	_, fch, _ := e.stream("/api/deployments/"+failed+"/log/stream", "")
	var sawFailureLine bool
	for {
		ev, ok := next(t, fch)
		if !ok {
			t.Fatal("stream closed without an end event")
		}
		if ev.name == "log" && strings.Contains(ev.data["text"].(string), "Deployment failed") {
			sawFailureLine = true
		}
		if ev.name == "end" {
			if ev.data["status"] != "failed" || !strings.Contains(ev.data["error"].(string), "manifest unknown") {
				t.Errorf("end = %+v", ev.data)
			}
			break
		}
	}
	if !sawFailureLine {
		t.Error("the failure line was not streamed")
	}
	e.deployer.Wait()

}

func TestReadFrom(t *testing.T) {
	path := t.TempDir() + "/log"
	if got, err := readFrom(path, 0); err != nil || got != "" {
		t.Fatalf("missing file = %q, %v", got, err)
	}
	if err := os.WriteFile(path, []byte("one\ntwo\npart"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := readFrom(path, 4); got != "two\npart" {
		t.Errorf("from 4 = %q", got)
	}
	if got, _ := readFrom(path, 100); got != "" {
		t.Errorf("past the end = %q", got)
	}
}
