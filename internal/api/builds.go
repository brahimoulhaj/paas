package api

import (
	"fmt"
	"math"
	"net/http"
	"runtime"

	"panel/internal/store"
)

// SetBuildDefaults sets the build limits that apply until they are changed in Settings
// (BERTH_BUILD_CPUS and BERTH_BUILD_MEMORY_MB).
func (s *Server) SetBuildDefaults(l store.BuildLimits) { s.buildDefaults = l }

// minBuildMemoryMB keeps BuildKit itself alive under a memory cap.
const minBuildMemoryMB = 256

type buildSettings struct {
	store.BuildLimits
	Defaults store.BuildLimits `json:"defaults"`  // from the environment, used until saved here
	HostCPUs int               `json:"host_cpus"` // upper bound for cpus
}

func (s *Server) buildSettings(r *http.Request) (buildSettings, error) {
	l, err := s.store.GetBuildLimits(r.Context(), s.buildDefaults)
	return buildSettings{BuildLimits: l, Defaults: s.buildDefaults, HostCPUs: runtime.NumCPU()}, err
}

func (s *Server) handleGetBuildSettings(w http.ResponseWriter, r *http.Request) {
	out, err := s.buildSettings(r)
	if err != nil {
		s.internal(w, "build settings", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePutBuildSettings saves the CPU and memory caps that Git builds share. They apply
// from the next build on; a build already running keeps the limits it started with.
func (s *Server) handlePutBuildSettings(w http.ResponseWriter, r *http.Request) {
	var in store.BuildLimits
	if !readJSON(w, r, &in) {
		return
	}
	cpus := runtime.NumCPU()
	switch {
	case math.IsNaN(in.CPUs) || in.CPUs < 0 || in.CPUs > float64(cpus):
		invalid(w, fmt.Sprintf("cpus must be between 0 (no cap) and %d, the cores of this server", cpus))
		return
	case in.CPUs > 0 && in.CPUs < 0.1:
		invalid(w, "cpus must be at least 0.1, or 0 for no cap")
		return
	case in.MemoryMB < 0 || (in.MemoryMB > 0 && in.MemoryMB < minBuildMemoryMB):
		invalid(w, fmt.Sprintf("memory_mb must be 0 (no cap) or at least %d", minBuildMemoryMB))
		return
	}
	in.CPUs = math.Round(in.CPUs*100) / 100
	if err := s.store.SetBuildLimits(r.Context(), in); err != nil {
		s.internal(w, "build settings", err)
		return
	}
	s.audit(r, "settings.builds", "settings", "builds", "", fmt.Sprintf("cpus=%g memory_mb=%d", in.CPUs, in.MemoryMB))
	s.handleGetBuildSettings(w, r)
}

// handleResetBuildSettings forgets the limits saved here: the environment's defaults apply again.
func (s *Server) handleResetBuildSettings(w http.ResponseWriter, r *http.Request) {
	for _, key := range []string{store.SettingBuildCPUs, store.SettingBuildMemory} {
		if err := s.store.DeleteSetting(r.Context(), key); err != nil {
			s.internal(w, "build settings", err)
			return
		}
	}
	s.audit(r, "settings.builds", "settings", "builds", "", "reset to defaults")
	s.handleGetBuildSettings(w, r)
}
