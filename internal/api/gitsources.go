package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"panel/internal/gitsource"
	"panel/internal/store"
)

// SetGitSources connects the Git connections service; without one, the endpoints answer
// 503 and apps can only use public repositories.
func (s *Server) SetGitSources(g *gitsource.Service) { s.git = g }

type gitSourceJSON struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Name       string    `json:"name"`
	BaseURL    string    `json:"base_url"`
	AppURL     string    `json:"app_url,omitempty"`     // GitHub: the app's page
	InstallURL string    `json:"install_url,omitempty"` // GitHub: choose accounts and repositories
	PublicKey  string    `json:"public_key,omitempty"`  // deploy key: add it to the repository
	CreatedAt  time.Time `json:"created_at"`
}

func toGitSourceJSON(c gitsource.Connection) gitSourceJSON {
	return gitSourceJSON{c.ID, c.Type, c.Name, c.BaseURL, c.AppURL, c.InstallURL, c.PublicKey, c.CreatedAt}
}

// withGit answers 503 when Git connections are not available.
func (s *Server) withGit(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.git == nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "Git connections are not available on this panel")
			return
		}
		next(w, r)
	}
}

// gitError answers failures of the Git connections service and reports whether it wrote
// a response.
func (s *Server) gitError(w http.ResponseWriter, what string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, gitsource.ErrInvalid):
		invalid(w, strings.TrimPrefix(err.Error(), gitsource.ErrInvalid.Error()+": "))
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "an app still uses this Git connection; change or delete the app first")
	default:
		// The Git host refused or could not be reached: the message says which, and holds no secret.
		s.log.Warn(what, "error", err)
		writeError(w, http.StatusBadGateway, "git_host", err.Error())
	}
	return true
}

func (s *Server) handleListGitSources(w http.ResponseWriter, r *http.Request) {
	list, err := s.git.List(r.Context())
	if err != nil {
		s.internal(w, "list git sources", err)
		return
	}
	writeJSON(w, http.StatusOK, mapSlice(list, toGitSourceJSON))
}

func (s *Server) handleAddGitLab(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Token   string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	c, err := s.git.AddGitLab(r.Context(), in.Name, in.BaseURL, in.Token)
	if s.gitError(w, "add gitlab connection", err) {
		return
	}
	s.audit(r, "git_source.add", "git_source", c.ID, c.Name, "GitLab "+c.BaseURL)
	writeJSON(w, http.StatusCreated, toGitSourceJSON(c))
}

// handleAddDeployKey generates an SSH key pair; the answer carries the public key to add
// to repositories. The private key never leaves the panel.
func (s *Server) handleAddDeployKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	c, err := s.git.AddDeployKey(r.Context(), in.Name)
	if s.gitError(w, "add deploy key", err) {
		return
	}
	s.audit(r, "git_source.add", "git_source", c.ID, c.Name, "deploy key")
	writeJSON(w, http.StatusCreated, toGitSourceJSON(c))
}

// handleStartGitHub returns the manifest the browser posts to GitHub to create the app.
func (s *Server) handleStartGitHub(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Origin       string `json:"origin"` // where the browser reaches the panel
		Organization string `json:"organization"`
		Name         string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	m, err := s.git.StartGitHub(in.Origin, in.Organization, in.Name)
	if s.gitError(w, "start github connection", err) {
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// handleGitHubCallback is where GitHub sends the browser once the app exists. It stores
// the app and goes on to GitHub's page that installs it on repositories; that page
// returns to Settings. A failure returns to Settings with the reason.
func (s *Server) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	c, err := s.git.FinishGitHub(r.Context(), q.Get("code"), q.Get("state"))
	if err != nil {
		s.log.Warn("github callback", "error", err)
		msg := strings.TrimPrefix(err.Error(), gitsource.ErrInvalid.Error()+": ")
		http.Redirect(w, r, "/settings?tab=git&git_error="+url.QueryEscape(msg), http.StatusFound)
		return
	}
	s.audit(r, "git_source.add", "git_source", c.ID, c.Name, "GitHub App")
	http.Redirect(w, r, c.InstallURL, http.StatusFound)
}

func (s *Server) handleDeleteGitSource(w http.ResponseWriter, r *http.Request) {
	c, err := s.git.Get(r.Context(), r.PathValue("id"))
	if s.gitError(w, "get git source", err) {
		return
	}
	if s.gitError(w, "delete git source", s.git.Delete(r.Context(), c.ID)) {
		return
	}
	s.audit(r, "git_source.remove", "git_source", c.ID, c.Name, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGitSourceRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.git.Repos(r.Context(), r.PathValue("id"), r.URL.Query().Get("q"))
	if s.gitError(w, "list repositories", err) {
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

func (s *Server) handleGitSourceBranches(w http.ResponseWriter, r *http.Request) {
	branches, err := s.git.Branches(r.Context(), r.PathValue("id"), r.URL.Query().Get("repo"))
	if s.gitError(w, "list branches", err) {
		return
	}
	writeJSON(w, http.StatusOK, branches)
}

// validGitSource checks an app's Git connection: it must exist and the repository must be
// on its host. It reports whether the app may be saved.
func (s *Server) validGitSource(w http.ResponseWriter, r *http.Request, in store.AppInput) bool {
	if in.SourceType != store.SourceGit {
		return true
	}
	if in.Git.ConnectionID == "" {
		if gitsource.SSHRepo(in.Git.Repository) {
			invalid(w, "an SSH repository URL needs a deploy key: choose one as git_source_id, or use the https URL")
			return false
		}
		return true
	}
	if s.git == nil {
		invalid(w, "git_source_id: Git connections are not available on this panel")
		return false
	}
	return !s.gitError(w, "check git source", s.git.CheckRepository(r.Context(), in.Git.ConnectionID, in.Git.Repository))
}
