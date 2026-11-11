package api

import (
	"errors"
	"net/http"
	"time"

	"panel/internal/account"
	"panel/internal/brand"
	"panel/internal/store"
)

type userJSON struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func toUserJSON(u store.User) userJSON {
	return userJSON{ID: u.ID, Email: u.Email, Role: u.Role, CreatedAt: u.CreatedAt}
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	u, err := s.accounts.CompleteSetup(r.Context(), in.Token, in.Email, in.Password)
	switch {
	case errors.Is(err, account.ErrSetupDone):
		writeError(w, http.StatusConflict, "setup_done", err.Error())
	case errors.Is(err, account.ErrBadSetupToken):
		writeError(w, http.StatusForbidden, "bad_setup_token", err.Error())
	case errors.Is(err, account.ErrInvalidEmail), errors.Is(err, account.ErrWeakPassword):
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
	case err != nil:
		s.internal(w, "setup", err)
	default:
		s.log.Info("owner created", "email", u.Email)
		s.startSession(w, r, u, http.StatusCreated)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	u, err := s.accounts.Login(r.Context(), in.Email, in.Password)
	switch {
	case errors.Is(err, account.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
	case err != nil:
		s.internal(w, "login", err)
	default:
		s.startSession(w, r, u, http.StatusOK)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(brand.SessionCookie()); err == nil && c.Value != "" {
		if err := s.accounts.EndSession(r.Context(), c.Value); err != nil {
			s.log.Error("logout", "error", err)
		}
	}
	http.SetCookie(w, sessionCookie(r, "", -1))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toUserJSON(userFrom(r.Context())))
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User, status int) {
	token, _, err := s.accounts.NewSession(r.Context(), u.ID)
	if err != nil {
		s.internal(w, "create session", err)
		return
	}
	http.SetCookie(w, sessionCookie(r, token, int(account.SessionTTL.Seconds())))
	writeJSON(w, status, toUserJSON(u))
}

// sessionCookie is HttpOnly and SameSite=Lax; Secure is set whenever the request came over HTTPS
// (directly or through the proxy), so the panel still works on http://<server-ip> before a domain is set.
func sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     brand.SessionCookie(),
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	}
}
