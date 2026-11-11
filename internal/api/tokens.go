package api

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"panel/internal/auth"
	"panel/internal/brand"
	"panel/internal/store"
)

type tokenJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	// Token is the secret itself, present only in the response that creates it.
	Token string `json:"token,omitempty"`
}

func toTokenJSON(t store.APIToken) tokenJSON {
	return tokenJSON{ID: t.ID, Name: t.Name, LastUsedAt: t.LastUsedAt, CreatedAt: t.CreatedAt}
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListAPITokens(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, "list tokens", err)
		return
	}
	out := make([]tokenJSON, 0, len(list))
	for _, t := range list {
		out = append(out, toTokenJSON(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateToken issues a token and returns it once. The prefix makes a leaked token
// easy to recognise (and to match in secret scanners).
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 64 {
		writeError(w, http.StatusUnprocessableEntity, "invalid", "name is required, at most 64 characters")
		return
	}
	secret := brand.Slug + "_" + auth.NewToken()
	t, err := s.store.CreateAPIToken(r.Context(), userFrom(r.Context()).ID, in.Name, auth.HashToken(secret))
	if err != nil {
		s.internal(w, "create token", err)
		return
	}
	s.audit(r, "token.create", "token", t.ID, t.Name, "")
	out := toTokenJSON(t)
	out.Token = secret
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.DeleteAPIToken(r.Context(), userFrom(r.Context()).ID, r.PathValue("id"))
	if s.storeError(w, "delete token", err, "") {
		return
	}
	s.audit(r, "token.revoke", "token", t.ID, t.Name, "")
	w.WriteHeader(http.StatusNoContent)
}
