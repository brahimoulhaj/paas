package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// sseStream writes Server-Sent Events. Every event is flushed at once, so a browser's
// EventSource sees log lines as they happen.
type sseStream struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// startSSE sends the stream headers. Nothing can change the status code after this.
func startSSE(w http.ResponseWriter) (*sseStream, error) {
	s := &sseStream{w: w, rc: http.NewResponseController(w)}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // tells buffering proxies to pass events through
	w.WriteHeader(http.StatusOK)
	if err := s.rc.Flush(); err != nil {
		return nil, err
	}
	return s, nil
}

// event sends one event. id may be empty; when set, a reconnecting browser sends it back
// as Last-Event-ID so the stream can resume where it stopped. data is sent as JSON, which
// never contains a raw newline, so it always fits the one-line "data:" field.
func (s *sseStream) event(id, name string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var b strings.Builder
	if id != "" {
		fmt.Fprintf(&b, "id: %s\n", id)
	}
	fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", name, body)
	if _, err := s.w.Write([]byte(b.String())); err != nil {
		return err
	}
	return s.rc.Flush()
}

// ping keeps the connection open through idle periods; clients ignore comment lines.
func (s *sseStream) ping() error {
	if _, err := s.w.Write([]byte(": ping\n\n")); err != nil {
		return err
	}
	return s.rc.Flush()
}
