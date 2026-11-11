package api

import (
	"context"
	"errors"
	"net/http"

	"panel/internal/store"
	"panel/internal/update"
)

// Updater keeps the panel itself up to date (internal/update).
type Updater interface {
	Info() update.Info
	Check(ctx context.Context) update.Info
	Apply(ctx context.Context) error
}

func (s *Server) SetUpdater(u Updater) { s.updater = u }

type updatesJSON struct {
	update.Info
	Auto bool `json:"auto"`
}

func (s *Server) updatesState(r *http.Request, info update.Info) (updatesJSON, error) {
	v, _, err := s.store.GetSetting(r.Context(), store.SettingUpdateAuto)
	return updatesJSON{Info: info, Auto: v == "true"}, err
}

func (s *Server) currentUpdates() update.Info {
	if s.updater == nil {
		return update.Info{Version: s.version, Status: update.StatusIdle, Reason: "updates are not set up on this panel"}
	}
	return s.updater.Info()
}

func (s *Server) handleGetUpdates(w http.ResponseWriter, r *http.Request) {
	out, err := s.updatesState(r, s.currentUpdates())
	if err != nil {
		s.internal(w, "updates", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	info := s.currentUpdates()
	if s.updater != nil {
		info = s.updater.Check(r.Context())
	}
	out, err := s.updatesState(r, info)
	if err != nil {
		s.internal(w, "updates", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleApplyUpdate pulls the new image and hands over to the update helper. The panel
// restarts a few seconds later; the UI waits for the new version to answer.
func (s *Server) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeError(w, http.StatusConflict, "unsupported", "updates are not set up on this panel")
		return
	}
	info := s.updater.Info()
	if err := s.updater.Apply(r.Context()); err != nil {
		code := http.StatusBadGateway // the pull or the helper failed
		if errors.Is(err, update.ErrBusy) || errors.Is(err, update.ErrNoUpdate) || errors.Is(err, update.ErrDeploying) {
			code = http.StatusConflict
		}
		writeError(w, code, "update_failed", err.Error())
		return
	}
	s.audit(r, "panel.update", "panel", "", info.Latest, info.Image+" → "+info.LatestImage)
	out, err := s.updatesState(r, s.updater.Info())
	if err != nil {
		s.internal(w, "updates", err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Auto bool `json:"auto"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	v := "false"
	if in.Auto {
		v = "true"
	}
	if err := s.store.SetSetting(r.Context(), store.SettingUpdateAuto, v); err != nil {
		s.internal(w, "updates", err)
		return
	}
	s.audit(r, "settings.updates", "settings", "updates", "", "automatic updates "+map[bool]string{true: "on", false: "off"}[in.Auto])
	out, err := s.updatesState(r, s.currentUpdates())
	if err != nil {
		s.internal(w, "updates", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
