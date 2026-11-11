// Package account implements owner setup, login and recovery. The HTTP API and the
// CLI both go through it, so the rules live in one place.
package account

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"sync"
	"time"

	"panel/internal/auth"
	"panel/internal/store"
)

const (
	SetupTokenTTL = 15 * time.Minute
	SessionTTL    = 30 * 24 * time.Hour
	MinPassword   = 8
	MaxPassword   = 256

	keySetupHash    = "setup_token_hash"
	keySetupExpires = "setup_token_expires_at"
)

var (
	ErrInvalidEmail       = errors.New("enter a valid email address")
	ErrWeakPassword       = errors.New("password must be at least 8 characters")
	ErrSetupDone          = errors.New("setup is already complete")
	ErrBadSetupToken      = errors.New("setup token is invalid or expired")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrEmailTaken         = errors.New("a user with this email already exists")
)

type Service struct {
	store *store.Store
	mu    sync.Mutex // serialises setup so two requests cannot both create the owner
}

func New(s *store.Store) *Service { return &Service{store: s} }

// SetupRequired is true until the first user exists.
func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	n, err := s.store.CountUsers(ctx)
	return n == 0, err
}

// IssueSetupToken creates a one-time setup token, replacing any earlier one.
func (s *Service) IssueSetupToken(ctx context.Context) (token string, expires time.Time, err error) {
	required, err := s.SetupRequired(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	if !required {
		return "", time.Time{}, ErrSetupDone
	}
	token = auth.NewToken()
	expires = time.Now().Add(SetupTokenTTL).UTC()
	if err = s.store.SetSetting(ctx, keySetupHash, auth.HashToken(token)); err != nil {
		return "", time.Time{}, err
	}
	if err = s.store.SetSetting(ctx, keySetupExpires, expires.Format(time.RFC3339)); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

func (s *Service) setupTokenValid(ctx context.Context, token string) (bool, error) {
	hash, ok, err := s.store.GetSetting(ctx, keySetupHash)
	if err != nil || !ok {
		return false, err
	}
	exp, ok, err := s.store.GetSetting(ctx, keySetupExpires)
	if err != nil || !ok {
		return false, err
	}
	expires, err := time.Parse(time.RFC3339, exp)
	if err != nil || time.Now().After(expires) {
		return false, nil
	}
	return token != "" && auth.HashToken(token) == hash, nil
}

func (s *Service) clearSetupToken(ctx context.Context) {
	_ = s.store.DeleteSetting(ctx, keySetupHash)
	_ = s.store.DeleteSetting(ctx, keySetupExpires)
}

// CompleteSetup creates the owner from the browser flow. It needs a valid setup
// token and only works while no user exists.
func (s *Service) CompleteSetup(ctx context.Context, token, email, password string) (store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	required, err := s.SetupRequired(ctx)
	if err != nil {
		return store.User{}, err
	}
	if !required {
		return store.User{}, ErrSetupDone
	}
	ok, err := s.setupTokenValid(ctx, token)
	if err != nil {
		return store.User{}, err
	}
	if !ok {
		return store.User{}, ErrBadSetupToken
	}
	u, err := s.createOwner(ctx, email, password)
	if err != nil {
		return store.User{}, err
	}
	s.clearSetupToken(ctx)
	return u, nil
}

// CreateOwner creates an owner from the CLI (no token; shell access is the proof).
func (s *Service) CreateOwner(ctx context.Context, email, password string) (store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, err := s.createOwner(ctx, email, password)
	if err != nil {
		return store.User{}, err
	}
	s.clearSetupToken(ctx)
	return u, nil
}

func (s *Service) createOwner(ctx context.Context, email, password string) (store.User, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return store.User{}, err
	}
	if err := checkPassword(password); err != nil {
		return store.User{}, err
	}
	if _, err := s.store.GetUserByEmail(ctx, email); err == nil {
		return store.User{}, ErrEmailTaken
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.User{}, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return store.User{}, err
	}
	return s.store.CreateUser(ctx, email, hash, "owner")
}

// ResetPassword sets a new password and signs the user out everywhere.
// An empty email targets the first owner.
func (s *Service) ResetPassword(ctx context.Context, email, password string) (store.User, error) {
	if err := checkPassword(password); err != nil {
		return store.User{}, err
	}
	var u store.User
	var err error
	if strings.TrimSpace(email) == "" {
		u, err = s.store.FirstOwner(ctx)
	} else {
		u, err = s.store.GetUserByEmail(ctx, strings.TrimSpace(email))
	}
	if err != nil {
		return store.User{}, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return store.User{}, err
	}
	if err := s.store.UpdateUserPassword(ctx, u.ID, hash); err != nil {
		return store.User{}, err
	}
	return u, s.store.DeleteUserSessions(ctx, u.ID)
}

// dummyHash keeps login timing the same whether or not the email exists.
var dummyHash, _ = auth.HashPassword("dummy-password-for-timing")

// Login checks credentials and returns the user.
func (s *Service) Login(ctx context.Context, email, password string) (store.User, error) {
	u, err := s.store.GetUserByEmail(ctx, strings.TrimSpace(email))
	if errors.Is(err, store.ErrNotFound) {
		_, _ = auth.VerifyPassword(dummyHash, password)
		return store.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return store.User{}, err
	}
	ok, err := auth.VerifyPassword(u.PasswordHash, password)
	if err != nil {
		return store.User{}, err
	}
	if !ok {
		return store.User{}, ErrInvalidCredentials
	}
	return u, nil
}

// NewSession stores a session for the user and returns the cookie token.
func (s *Service) NewSession(ctx context.Context, userID string) (token string, expires time.Time, err error) {
	token = auth.NewToken()
	expires = time.Now().Add(SessionTTL)
	return token, expires, s.store.CreateSession(ctx, userID, auth.HashToken(token), expires)
}

func (s *Service) SessionUser(ctx context.Context, token string) (store.User, error) {
	return s.store.SessionUser(ctx, auth.HashToken(token))
}

func (s *Service) EndSession(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, auth.HashToken(token))
}

func normalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 || !strings.Contains(email[strings.LastIndex(email, "@")+1:], ".") {
		return "", ErrInvalidEmail
	}
	return strings.ToLower(email), nil
}

func checkPassword(p string) error {
	if len(p) < MinPassword || len(p) > MaxPassword {
		return ErrWeakPassword
	}
	return nil
}
