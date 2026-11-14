package api

import (
	"fmt"
	"net/http"
	"strings"

	"panel/internal/store"
)

type healthCheckJSON struct {
	store.HealthCheck
	Saved bool `json:"saved"` // false: the app keeps the behaviour from before health checks
}

func (s *Server) handleGetHealthCheck(w http.ResponseWriter, r *http.Request) {
	h, saved, err := s.store.AppHealthCheck(r.Context(), r.PathValue("id"))
	if s.storeError(w, "health check", err, "") {
		return
	}
	writeJSON(w, http.StatusOK, healthCheckJSON{HealthCheck: h, Saved: saved})
}

// handleSetHealthCheck saves the check the next deploys wait for.
func (s *Server) handleSetHealthCheck(w http.ResponseWriter, r *http.Request) {
	var in store.HealthCheck
	if !readJSON(w, r, &in) {
		return
	}
	a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get app", err, "") {
		return
	}
	if msg := validHealthCheck(&in); msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_health_check", msg)
		return
	}
	if s.storeError(w, "health check", s.store.SetAppHealthCheck(r.Context(), a.ID, in), "") {
		return
	}
	detail := in.Type
	if in.Type == store.HealthHTTP {
		detail += " " + in.Path
	}
	s.audit(r, "app.health_check", "app", a.ID, a.Name, detail)
	writeJSON(w, http.StatusOK, healthCheckJSON{HealthCheck: in, Saved: true})
}

// validHealthCheck says what is wrong with h, or "". It trims the path.
func validHealthCheck(h *store.HealthCheck) string {
	inRange := func(name string, v, lo, hi int) string {
		if v < lo || v > hi {
			return fmt.Sprintf("%s must be between %d and %d", name, lo, hi)
		}
		return ""
	}
	h.Path = strings.TrimSpace(h.Path)
	switch h.Type {
	case store.HealthTCP, store.HealthRunning:
	case store.HealthHTTP:
		if !strings.HasPrefix(h.Path, "/") || len(h.Path) > 512 || strings.ContainsFunc(h.Path, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
			return "path must start with / and hold no spaces"
		}
		if h.ExpectedStatus != 0 {
			if msg := inRange("expected status", h.ExpectedStatus, 100, 599); msg != "" {
				return msg
			}
		}
	default:
		return "type must be tcp, http or running"
	}
	if h.Type == store.HealthRunning {
		return inRange("time to stay up", h.StartPeriod, 1, 900)
	}
	for _, msg := range []string{
		inRange("interval", h.Interval, 1, 300),
		inRange("timeout", h.Timeout, 1, 60),
		inRange("retries", h.Retries, 1, 20),
		inRange("start period", h.StartPeriod, 0, 900),
	} {
		if msg != "" {
			return msg
		}
	}
	return ""
}
