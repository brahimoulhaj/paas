package gitsource

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"panel/internal/brand"
	"panel/internal/store"
)

// Manifest is what the browser needs to create the GitHub App: it posts Manifest as the
// form field "manifest" to ActionURL, and GitHub sends the user back to the callback.
type Manifest struct {
	ActionURL string `json:"action_url"`
	Manifest  string `json:"manifest"` // JSON
	State     string `json:"state"`
}

var (
	orgRe     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	appNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,33}$`)
	codeRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)
)

// manifestTTL is how long GitHub keeps the code of a manifest; a started connection is
// remembered for as long.
const manifestTTL = time.Hour

// CallbackPath is where GitHub sends the browser after the app is created.
const CallbackPath = "/api/git-sources/github/callback"

// StartGitHub prepares the creation of a GitHub App owned by the user (or by
// organization, when set). origin is the address the browser reaches the panel at; the
// callback and the app's links are built from it. The app can read code and metadata of
// the repositories it is installed on, nothing else.
func (s *Service) StartGitHub(origin, organization, name string) (Manifest, error) {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.Trim(u.Path, "/") != "" || u.User != nil {
		return Manifest{}, invalidf("origin must be the address of this panel, like https://panel.example.com")
	}
	origin = u.Scheme + "://" + u.Host
	organization = strings.TrimSpace(organization)
	if organization != "" && !orgRe.MatchString(organization) {
		return Manifest{}, invalidf("organization must be a GitHub organization name")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = brand.Slug + "-" + randomHex(3) // app names are unique across GitHub
	}
	if !appNameRe.MatchString(name) {
		return Manifest{}, invalidf("name must be 1 to 34 letters, digits, spaces, dots, dashes or underscores")
	}

	manifest, err := json.Marshal(map[string]any{
		"name":                name,
		"url":                 origin,
		"redirect_url":        origin + CallbackPath,
		"setup_url":           origin + "/settings",
		"public":              false,
		"default_permissions": map[string]string{"contents": "read", "metadata": "read"},
		"default_events":      []string{},
	})
	if err != nil {
		return Manifest{}, err
	}

	state := randomHex(16)
	s.mu.Lock()
	for k, exp := range s.pending {
		if time.Now().After(exp) {
			delete(s.pending, k)
		}
	}
	s.pending[state] = time.Now().Add(manifestTTL)
	s.mu.Unlock()

	action := s.GitHubURL + "/settings/apps/new"
	if organization != "" {
		action = s.GitHubURL + "/organizations/" + organization + "/settings/apps/new"
	}
	return Manifest{ActionURL: action + "?state=" + state, Manifest: string(manifest), State: state}, nil
}

// FinishGitHub completes a connection started by StartGitHub: it exchanges the code
// GitHub passed to the callback for the new app's keys and stores them.
func (s *Service) FinishGitHub(ctx context.Context, code, state string) (Connection, error) {
	s.mu.Lock()
	exp, ok := s.pending[state]
	delete(s.pending, state)
	s.mu.Unlock()
	if !ok || time.Now().After(exp) {
		return Connection{}, ErrState
	}
	if !codeRe.MatchString(code) {
		return Connection{}, invalidf("GitHub sent no usable code")
	}
	var app struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		Name          string `json:"name"`
		HTMLURL       string `json:"html_url"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		WebhookSecret string `json:"webhook_secret"`
		PEM           string `json:"pem"`
	}
	if _, err := s.call(ctx, "POST", s.GitHubAPI+"/app-manifests/"+code+"/conversions", githubHeaders(""), &app); err != nil {
		return Connection{}, err
	}
	if app.ID == 0 || app.Slug == "" || app.PEM == "" {
		return Connection{}, errors.New("GitHub answered without the app's keys")
	}
	if _, err := parseKey(app.PEM); err != nil {
		return Connection{}, err
	}
	return s.save(ctx, store.GitTypeGitHubApp, app.Name, s.GitHubURL, creds{
		AppID: app.ID, Slug: app.Slug, HTMLURL: app.HTMLURL, ClientID: app.ClientID,
		ClientSecret: app.ClientSecret, WebhookSecret: app.WebhookSecret, PrivateKey: app.PEM,
	})
}

func (s *Service) installURL(slug string) string {
	return s.GitHubURL + "/apps/" + url.PathEscape(slug) + "/installations/new"
}

func githubHeaders(bearer string) map[string]string {
	h := map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"}
	if bearer != "" {
		h["Authorization"] = "Bearer " + bearer
	}
	return h
}

func parseKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("the GitHub App's private key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	any, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the GitHub App's private key: %w", err)
	}
	key, ok := any.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the GitHub App's private key is not an RSA key")
	}
	return key, nil
}

// appJWT signs the short-lived token a GitHub App authenticates as itself with.
func appJWT(c creds, now time.Time) (string, error) {
	key, err := parseKey(c.PrivateKey)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(), // GitHub's advice, against clock drift
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(c.AppID, 10),
	})
	signed := enc([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signed + "." + enc(sig), nil
}

// installations returns the ids of the accounts the app is installed on.
func (s *Service) installations(ctx context.Context, c creds) ([]int64, error) {
	jwt, err := appJWT(c, time.Now())
	if err != nil {
		return nil, err
	}
	var list []struct {
		ID int64 `json:"id"`
	}
	if _, err := s.call(ctx, "GET", s.GitHubAPI+"/app/installations?per_page=100", githubHeaders(jwt), &list); err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(list))
	for _, i := range list {
		out = append(out, i.ID)
	}
	return out, nil
}

// installToken returns an access token for one installation. GitHub's tokens live an
// hour; one is reused until five minutes before it ends.
func (s *Service) installToken(ctx context.Context, connID string, c creds, installation int64) (string, error) {
	key := connID + "/" + strconv.FormatInt(installation, 10)
	s.mu.Lock()
	cached, ok := s.tokens[key]
	s.mu.Unlock()
	if ok && time.Until(cached.expires) > 5*time.Minute {
		return cached.token, nil
	}
	jwt, err := appJWT(c, time.Now())
	if err != nil {
		return "", err
	}
	var res struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	u := s.GitHubAPI + "/app/installations/" + strconv.FormatInt(installation, 10) + "/access_tokens"
	if _, err := s.call(ctx, "POST", u, githubHeaders(jwt), &res); err != nil {
		return "", err
	}
	if res.Token == "" {
		return "", errors.New("GitHub answered without a token")
	}
	s.mu.Lock()
	s.tokens[key] = cachedToken{token: res.Token, expires: res.ExpiresAt}
	s.mu.Unlock()
	return res.Token, nil
}

// repoToken returns a token that can read fullName, through the installation that covers it.
func (s *Service) repoToken(ctx context.Context, connID string, c creds, fullName string) (string, error) {
	jwt, err := appJWT(c, time.Now())
	if err != nil {
		return "", err
	}
	var inst struct {
		ID int64 `json:"id"`
	}
	_, err = s.call(ctx, "GET", s.GitHubAPI+"/repos/"+fullName+"/installation", githubHeaders(jwt), &inst)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == 404 {
		return "", fmt.Errorf("the GitHub App is not installed on %s; add the repository to the app's installation", fullName)
	}
	if err != nil {
		return "", err
	}
	return s.installToken(ctx, connID, c, inst.ID)
}

// githubRepos lists the repositories of every installation, up to 1000 per installation.
func (s *Service) githubRepos(ctx context.Context, connID string, c creds) ([]Repo, error) {
	ids, err := s.installations(ctx, c)
	if err != nil {
		return nil, err
	}
	out := []Repo{}
	for _, id := range ids {
		token, err := s.installToken(ctx, connID, c, id)
		if err != nil {
			return nil, err
		}
		for page := 1; page <= 10; page++ {
			var res struct {
				Repositories []struct {
					FullName      string `json:"full_name"`
					CloneURL      string `json:"clone_url"`
					DefaultBranch string `json:"default_branch"`
					Private       bool   `json:"private"`
				} `json:"repositories"`
			}
			u := s.GitHubAPI + "/installation/repositories?per_page=100&page=" + strconv.Itoa(page)
			if _, err := s.call(ctx, "GET", u, githubHeaders(token), &res); err != nil {
				return nil, err
			}
			for _, r := range res.Repositories {
				out = append(out, Repo{FullName: r.FullName, CloneURL: r.CloneURL, DefaultBranch: r.DefaultBranch, Private: r.Private})
			}
			if len(res.Repositories) < 100 {
				break
			}
		}
	}
	return out, nil
}

func (s *Service) githubBranches(ctx context.Context, connID string, c creds, fullName string) ([]string, error) {
	token, err := s.repoToken(ctx, connID, c, fullName)
	if err != nil {
		return nil, err
	}
	var branches []struct {
		Name string `json:"name"`
	}
	if _, err := s.call(ctx, "GET", s.GitHubAPI+"/repos/"+fullName+"/branches?per_page=100", githubHeaders(token), &branches); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(branches))
	for _, b := range branches {
		out = append(out, b.Name)
	}
	return out, nil
}
