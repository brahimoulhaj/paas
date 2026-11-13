package api

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"panel/internal/executor"
	"panel/internal/store"
)

const (
	ssePing        = 15 * time.Second
	logTailDefault = 200
	logTailMax     = 5000
)

// logEvent is one line in a log stream.
type logEvent struct {
	Stream string `json:"stream,omitempty"` // stdout or stderr; empty for deployment logs
	Time   string `json:"time,omitempty"`   // RFC 3339
	Text   string `json:"text"`
}

// handleAppLogStream streams the output of the app's current container as Server-Sent
// Events: "log" for each line, then "end" when the container stops. A reconnecting
// browser sends the time of the last line it saw and gets only what came after.
func (s *Server) handleAppLogStream(w http.ResponseWriter, r *http.Request) {
	if !s.requireDeployer(w) {
		return
	}
	a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get app", err, "") {
		return
	}
	opts := executor.LogOptions{Follow: true, Tail: logTailDefault}
	if v := r.URL.Query().Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > logTailMax {
			invalid(w, "tail must be between 1 and "+strconv.Itoa(logTailMax))
			return
		}
		opts.Tail = n
	}
	if last, err := time.Parse(time.RFC3339Nano, r.Header.Get("Last-Event-ID")); err == nil {
		opts.Since, opts.Tail = last.Add(time.Nanosecond), 0
	}
	// Refuse with a normal error response while that is still possible.
	if state := s.deployer.RuntimeState(r.Context(), a.ID); state == "none" || state == "missing" {
		writeError(w, http.StatusConflict, "not_deployed", "the app has no container; deploy it first")
		return
	}

	stream, err := startSSE(w)
	if err != nil {
		s.log.Error("log stream", "error", err)
		return
	}
	ctx, cancel := s.streamContext(r)
	defer cancel()
	lines := make(chan executor.LogLine, 256)
	done := make(chan error, 1)
	go func() {
		done <- s.deployer.StreamLogs(ctx, a.ID, opts, func(l executor.LogLine) {
			select {
			case lines <- l: // a slow browser slows the reader down; nothing is dropped
			case <-ctx.Done():
			}
		})
	}()

	send := func(l executor.LogLine) error {
		ev := logEvent{Stream: l.Stream, Text: l.Text}
		id := ""
		if !l.Time.IsZero() {
			id = l.Time.UTC().Format(time.RFC3339Nano)
			ev.Time = id
		}
		return stream.event(id, "log", ev)
	}
	ping := time.NewTicker(ssePing)
	defer ping.Stop()
	for {
		select {
		case l := <-lines:
			if send(l) != nil {
				return
			}
		case err := <-done:
			for len(lines) > 0 { // what was read before the stream ended
				if send(<-lines) != nil {
					return
				}
			}
			reason := "the container stopped"
			if err != nil && ctx.Err() == nil {
				s.log.Error("log stream", "app", a.Name, "error", err)
				reason = "the log stream failed"
			}
			_ = stream.event("", "end", map[string]string{"reason": reason})
			return
		case <-ping.C:
			if stream.ping() != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// handleDeploymentLogStream follows a deployment's log file: everything written so far,
// then new lines as the deployment produces them, then "end" with the final status. Event
// ids are byte offsets into the file, so a reconnect resumes exactly where it stopped.
func (s *Server) handleDeploymentLogStream(w http.ResponseWriter, r *http.Request) {
	if !s.requireDeployer(w) {
		return
	}
	dep, err := s.store.GetDeployment(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get deployment", err, "") {
		return
	}
	var offset int64
	if n, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil && n > 0 {
		offset = n
	}
	path := s.deployer.LogPath(dep)

	stream, err := startSSE(w)
	if err != nil {
		s.log.Error("deployment log stream", "error", err)
		return
	}
	ctx, cancel := s.streamContext(r)
	defer cancel()
	poll := time.NewTicker(s.logPoll)
	defer poll.Stop()
	ping := time.NewTicker(ssePing)
	defer ping.Stop()

	for {
		// Status first, file second: once the status says "finished", the file is complete,
		// so reading it afterwards cannot miss the last lines.
		finished := dep.Finished()
		chunk, err := readFrom(path, offset)
		if err != nil {
			s.log.Error("deployment log stream", "error", err)
			_ = stream.event("", "end", map[string]string{"status": dep.Status, "error": "the log could not be read"})
			return
		}
		for {
			i := strings.IndexByte(chunk, '\n')
			if i < 0 {
				break // a line still being written: picked up whole on the next pass
			}
			line := chunk[:i]
			chunk = chunk[i+1:]
			offset += int64(i + 1)
			ev := logEvent{Text: line}
			if ts, rest, ok := strings.Cut(line, " "); ok {
				if _, err := time.Parse(time.RFC3339Nano, ts); err == nil {
					ev.Time, ev.Text = ts, rest
				}
			}
			if stream.event(strconv.FormatInt(offset, 10), "log", ev) != nil {
				return
			}
		}
		if finished {
			_ = stream.event("", "end", map[string]string{"status": dep.Status, "error": dep.Error})
			return
		}
		select {
		case <-poll.C:
		case <-ping.C:
			if stream.ping() != nil {
				return
			}
		case <-ctx.Done():
			return
		}
		dep, err = s.store.GetDeployment(ctx, dep.ID)
		if err != nil { // deleted with its app, or the request ended
			if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
				s.log.Error("deployment log stream", "error", err)
			}
			_ = stream.event("", "end", map[string]string{"status": "", "error": "the deployment no longer exists"})
			return
		}
	}
}

// readFrom returns the file's content from offset on; a file that does not exist yet
// (deployment still queued) is empty.
func readFrom(path string, offset int64) (string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(f, 4<<20)) // at most 4 MiB per pass; the rest follows
	return string(b), err
}
