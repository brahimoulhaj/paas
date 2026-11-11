package api

import (
	"context"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"panel/internal/proxy"
	"panel/internal/store"
)

// ProxyApplier pushes the routing state in the database to the running proxy.
type ProxyApplier interface {
	Apply(ctx context.Context) error
}

// SetProxy connects the proxy; without one, settings are saved and applied on its next start.
func (s *Server) SetProxy(p ProxyApplier) { s.proxy = p }

type panelSettings struct {
	Domain    string `json:"domain"`
	ACMEEmail string `json:"acme_email"`
}

func (s *Server) handleGetPanelSettings(w http.ResponseWriter, r *http.Request) {
	domain, _, err := s.store.GetSetting(r.Context(), store.SettingPanelDomain)
	if err != nil {
		s.internal(w, "panel settings", err)
		return
	}
	email, _, err := s.store.GetSetting(r.Context(), store.SettingACMEEmail)
	if err != nil {
		s.internal(w, "panel settings", err)
		return
	}
	writeJSON(w, http.StatusOK, panelSettings{Domain: domain, ACMEEmail: email})
}

// handlePutPanelSettings sets the panel's own domain (served over HTTPS with an automatic
// certificate) and the certificate account email. An empty domain removes it; the panel
// stays reachable on http://<server-ip> either way.
func (s *Server) handlePutPanelSettings(w http.ResponseWriter, r *http.Request) {
	var in panelSettings
	if !readJSON(w, r, &in) {
		return
	}
	in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
	in.ACMEEmail = strings.TrimSpace(in.ACMEEmail)
	if in.Domain != "" {
		if err := proxy.ValidHost(in.Domain); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid", "domain must be a hostname like panel.example.com")
			return
		}
	}
	if in.ACMEEmail != "" {
		if a, err := mail.ParseAddress(in.ACMEEmail); err != nil || a.Address != in.ACMEEmail {
			writeError(w, http.StatusUnprocessableEntity, "invalid", "acme_email must be an email address")
			return
		}
	}
	if in.Domain != "" {
		taken, err := s.store.DomainExists(r.Context(), in.Domain)
		if err != nil {
			s.internal(w, "panel settings", err)
			return
		}
		if taken {
			writeError(w, http.StatusConflict, "conflict", "this hostname is already used by an app")
			return
		}
	}
	for key, value := range map[string]string{store.SettingPanelDomain: in.Domain, store.SettingACMEEmail: in.ACMEEmail} {
		if err := s.store.SetSetting(r.Context(), key, value); err != nil {
			s.internal(w, "panel settings", err)
			return
		}
	}
	s.audit(r, "settings.panel", "settings", "panel", in.Domain, "")
	if !s.applyProxy(r.Context()) {
		writeError(w, http.StatusServiceUnavailable, "proxy_unavailable",
			"saved, but the proxy could not be updated; it is applied when the proxy is back")
		return
	}
	writeJSON(w, http.StatusOK, in)
}

// handlePanelDNS is the DNS hint for the panel's own domain (see handleDomainDNS).
func (s *Server) handlePanelDNS(w http.ResponseWriter, r *http.Request) {
	domain, _, err := s.store.GetSetting(r.Context(), store.SettingPanelDomain)
	if err != nil {
		s.internal(w, "panel settings", err)
		return
	}
	if domain == "" {
		writeError(w, http.StatusNotFound, "not_found", "no panel domain is set")
		return
	}
	writeJSON(w, http.StatusOK, s.dnsHint(r.Context(), domain))
}

// handleGetOnboarding tells the UI whether to show the onboarding steps.
func (s *Server) handleGetOnboarding(w http.ResponseWriter, r *http.Request) {
	_, done, err := s.store.GetSetting(r.Context(), store.SettingOnboarded)
	if err != nil {
		s.internal(w, "onboarding", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"completed": done})
}

// handleCompleteOnboarding records that the owner finished or skipped onboarding. The
// steps stay reachable afterwards; they are just no longer shown on their own.
func (s *Server) handleCompleteOnboarding(w http.ResponseWriter, r *http.Request) {
	if _, done, err := s.store.GetSetting(r.Context(), store.SettingOnboarded); err != nil {
		s.internal(w, "onboarding", err)
		return
	} else if done {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.SetSetting(r.Context(), store.SettingOnboarded, time.Now().UTC().Format(time.RFC3339)); err != nil {
		s.internal(w, "onboarding", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
