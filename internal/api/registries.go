package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"panel/internal/executor"
	"panel/internal/store"
)

// RegistryChecker logs in to a registry to check credentials; *executor.Docker implements it.
type RegistryChecker interface {
	RegistryLogin(ctx context.Context, auth executor.RegistryAuth) error
}

// SetRegistryChecker connects the credential check; without one, registries are saved
// unchecked.
func (s *Server) SetRegistryChecker(c RegistryChecker) { s.registryCheck = c }

type registryJSON struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Server    string    `json:"server"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

func toRegistryJSON(r store.Registry) registryJSON {
	return registryJSON{ID: r.ID, Name: r.Name, Server: r.Server, Username: r.Username, CreatedAt: r.CreatedAt}
}

// registryServerRe is a registry host with an optional port: no scheme, no path.
var registryServerRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:[0-9]{1,5})?$`)

func (s *Server) handleListRegistries(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListRegistries(r.Context())
	if err != nil {
		s.internal(w, "list registries", err)
		return
	}
	writeJSON(w, http.StatusOK, mapSlice(list, toRegistryJSON))
}

// handleAddRegistry checks the credentials with a login, then stores them; the password
// is encrypted and never returned.
func (s *Server) handleAddRegistry(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Server   string `json:"server"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	c := store.RegistryCredentials{
		Server:   store.NormalizeRegistryServer(in.Server),
		Username: strings.TrimSpace(in.Username),
		Password: in.Password,
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = c.Server
	}
	switch {
	case len(c.Server) > 253 || !registryServerRe.MatchString(c.Server):
		invalid(w, "server must be a registry host like ghcr.io or registry.example.com:5000 (docker.io for Docker Hub)")
		return
	case c.Username == "" || len(c.Username) > 256 || strings.ContainsAny(c.Username, " \t\r\n"):
		invalid(w, "username is required")
		return
	case c.Password == "" || len(c.Password) > 4096 || strings.ContainsAny(c.Password, "\r\n"):
		invalid(w, "password (or access token) is required")
		return
	case len(name) > 64:
		invalid(w, "name must be at most 64 characters")
		return
	}
	if s.registryCheck != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		err := s.registryCheck.RegistryLogin(ctx, executor.RegistryAuth{Server: c.Server, Username: c.Username, Password: c.Password})
		cancel()
		switch {
		case errors.Is(err, executor.ErrUnauthorized):
			invalid(w, c.Server+" refused the username or password")
			return
		case err != nil:
			writeError(w, http.StatusBadGateway, "registry", "could not sign in to "+c.Server+": "+err.Error())
			return
		}
	}
	reg, err := s.store.CreateRegistry(r.Context(), name, c)
	if err != nil {
		s.internal(w, "add registry", err)
		return
	}
	s.audit(r, "registry.add", "registry", reg.ID, reg.Name, reg.Username+"@"+reg.Server)
	writeJSON(w, http.StatusCreated, toRegistryJSON(reg))
}

func (s *Server) handleDeleteRegistry(w http.ResponseWriter, r *http.Request) {
	reg, err := s.store.GetRegistry(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get registry", err, "") {
		return
	}
	err = s.store.DeleteRegistry(r.Context(), reg.ID)
	if s.storeError(w, "delete registry", err, "an app still pulls from this registry; change or delete the app first") {
		return
	}
	s.audit(r, "registry.remove", "registry", reg.ID, reg.Name, reg.Server)
	w.WriteHeader(http.StatusNoContent)
}

// validRegistry checks an image app's registry: it must exist, and the image must be on
// its host, so the credentials only ever go to their own registry.
func (s *Server) validRegistry(w http.ResponseWriter, r *http.Request, in store.AppInput) bool {
	if in.SourceType == store.SourceGit || in.RegistryID == "" {
		return true
	}
	reg, err := s.store.GetRegistry(r.Context(), in.RegistryID)
	if errors.Is(err, store.ErrNotFound) {
		invalid(w, "registry_id is not a registry of this panel")
		return false
	}
	if err != nil {
		s.internal(w, "get registry", err)
		return false
	}
	if host := store.ImageServer(in.Image); host != reg.Server {
		invalid(w, "the image is on "+host+", but the registry is "+reg.Server+": use an image like "+reg.Server+"/org/app:tag")
		return false
	}
	return true
}
