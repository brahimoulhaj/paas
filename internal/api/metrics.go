package api

import (
	"net/http"

	"panel/internal/hostmetrics"
)

type HostMetrics interface{ Snapshot() hostmetrics.Snapshot }

func (s *Server) SetHostMetrics(metrics HostMetrics) { s.metrics = metrics }

func (s *Server) handleHostMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.metrics == nil {
		writeError(w, http.StatusServiceUnavailable, "metrics_unavailable", "host metrics are starting up")
		return
	}
	snapshot := s.metrics.Snapshot()
	if snapshot.CollectedAt.IsZero() {
		writeError(w, http.StatusServiceUnavailable, "metrics_unavailable", "host metrics are starting up")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}
