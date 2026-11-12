package api

import (
	"net/http"

	"panel/internal/dockermetrics"
)

type DockerMetrics interface{ Snapshot() dockermetrics.Snapshot }

func (s *Server) SetDockerMetrics(metrics DockerMetrics) { s.dockerMetrics = metrics }

func (s *Server) handleDockerMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.dockerMetrics != nil {
		snapshot := s.dockerMetrics.Snapshot()
		if !snapshot.CheckedAt.IsZero() {
			writeJSON(w, http.StatusOK, snapshot)
			return
		}
	}
	writeError(w, http.StatusServiceUnavailable, "metrics_unavailable", "Docker metrics are starting up")
}
