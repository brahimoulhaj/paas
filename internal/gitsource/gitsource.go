// Package gitsource connects the panel to Git hosts so it can list and fetch private
// repositories: GitHub through a GitHub App created from the panel (the manifest flow),
// GitLab through an access token, any host over SSH through a deploy key the panel
// generates. Secrets are stored encrypted in git_sources.
package gitsource

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"panel/internal/builder"
	"panel/internal/secret"
	"panel/internal/store"
)

var (
	// ErrInvalid wraps problems with what the user entered; its message is shown to them.
	ErrInvalid = errors.New("invalid")
	// ErrState means a GitHub callback did not match a connection started from this panel.
	ErrState = errors.New("this GitHub connection was not started here, or it expired; start again")
)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Connection is a Git connection without its secrets.
type Connection struct {
	ID         string
	Type       string
	Name       string
	BaseURL    string
	AppURL     string // GitHub: the app's settings page
	InstallURL string // GitHub: where the app is installed on accounts and repositories
	PublicKey  string // deploy key: the line to add to a repository
	CreatedAt  time.Time
}

// Repo is a repository a connection can read.
type Repo struct {
	FullName      string `json:"full_name"` // owner/name, or group/subgroup/name
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// creds is the decrypted secret column.
type creds struct {
	// GitHub App
	AppID         int64  `json:"app_id,omitempty"`
	Slug          string `json:"slug,omitempty"`
	HTMLURL       string `json:"html_url,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	ClientSecret  string `json:"client_secret,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
	PrivateKey    string `json:"private_key,omitempty"` // PEM
	// GitLab
	Token string `json:"token,omitempty"`
	// Deploy key (PrivateKey above holds its private half)
	PublicKey string `json:"public_key,omitempty"`
}

type Service struct {
	st   *store.Store
	box  *secret.Box
	http *http.Client

	// GitHub's addresses; tests point them at a fake.
	GitHubURL string // https://github.com
	GitHubAPI string // https://api.github.com

	mu      sync.Mutex
	pending map[string]time.Time   // manifest states → expiry
	tokens  map[string]cachedToken // installation tokens, by connection id and installation
}

type cachedToken struct {
	token   string
	expires time.Time
}

func New(st *store.Store, box *secret.Box) *Service {
	return &Service{
		st: st, box: box,
		http:      &http.Client{Timeout: 20 * time.Second},
		GitHubURL: "https://github.com",
		GitHubAPI: "https://api.github.com",
		pending:   map[string]time.Time{},
		tokens:    map[string]cachedToken{},
	}
}

// ---- connections ----

func (s *Service) load(ctx context.Context, id string) (store.GitConnection, creds, error) {
	row, err := s.st.GetGitConnection(ctx, id)
	if err != nil {
		return row, creds{}, err
	}
	c, err := s.open(row)
	return row, c, err
}

func (s *Service) open(row store.GitConnection) (creds, error) {
	var c creds
	plain, err := s.box.Decrypt(row.Secret)
	if err != nil {
		return c, fmt.Errorf("git connection %s: %w", row.Name, err)
	}
	return c, json.Unmarshal(plain, &c)
}

func (s *Service) save(ctx context.Context, typ, name, baseURL string, c creds) (Connection, error) {
	plain, err := json.Marshal(c)
	if err != nil {
		return Connection{}, err
	}
	sealed, err := s.box.Encrypt(plain)
	if err != nil {
		return Connection{}, err
	}
	row, err := s.st.CreateGitConnection(ctx, typ, name, baseURL, sealed)
	if err != nil {
		return Connection{}, err
	}
	return s.public(row, c), nil
}

func (s *Service) public(row store.GitConnection, c creds) Connection {
	out := Connection{ID: row.ID, Type: row.Type, Name: row.Name, BaseURL: row.BaseURL, CreatedAt: row.CreatedAt}
	switch row.Type {
	case store.GitTypeGitHubApp:
		out.AppURL = c.HTMLURL
		out.InstallURL = s.installURL(c.Slug)
	case store.GitTypeDeployKey:
		out.PublicKey = c.PublicKey
	}
	return out
}

func (s *Service) List(ctx context.Context) ([]Connection, error) {
	rows, err := s.st.ListGitConnections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Connection, 0, len(rows))
	for _, row := range rows {
		c, err := s.open(row)
		if err != nil {
			return nil, err
		}
		out = append(out, s.public(row, c))
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, id string) (Connection, error) {
	row, c, err := s.load(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	return s.public(row, c), nil
}

// Delete forgets a connection; store.ErrConflict while an app still uses it. A GitHub
// App is not deleted on GitHub: only its owner can do that, from the app's settings page.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.st.DeleteGitConnection(ctx, id)
}

// Credentials returns what a clone of repository needs. The repository must be on the
// connection's host, so a token is never offered to another one.
func (s *Service) Credentials(ctx context.Context, id, repository string) (builder.Auth, error) {
	row, c, err := s.load(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return builder.Auth{}, errors.New("the app's Git connection was removed")
	}
	if err != nil {
		return builder.Auth{}, err
	}
	if row.Type == store.GitTypeDeployKey {
		if !SSHRepo(repository) {
			return builder.Auth{}, errors.New("a deploy key clones over SSH: the repository must be an SSH URL like git@github.com:org/app.git")
		}
		return builder.Auth{SSHKey: c.PrivateKey}, nil
	}
	fullName, err := repoOnHost(row.BaseURL, repository)
	if err != nil {
		return builder.Auth{}, err
	}
	switch row.Type {
	case store.GitTypeGitLab:
		return builder.Auth{Username: "oauth2", Password: c.Token}, nil
	case store.GitTypeGitHubApp:
		token, err := s.repoToken(ctx, row.ID, c, fullName)
		if err != nil {
			return builder.Auth{}, err
		}
		return builder.Auth{Username: "x-access-token", Password: token}, nil
	}
	return builder.Auth{}, fmt.Errorf("unknown git connection type %q", row.Type)
}

// CheckRepository reports (as ErrInvalid) why repository cannot be fetched through the
// connection: unknown connection, or a URL on another host.
func (s *Service) CheckRepository(ctx context.Context, id, repository string) error {
	row, err := s.st.GetGitConnection(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return invalidf("git_source_id is not a Git connection of this panel")
	}
	if err != nil {
		return err
	}
	if row.Type == store.GitTypeDeployKey {
		if !SSHRepo(repository) {
			return invalidf("with a deploy key, git_repository must be an SSH URL like git@github.com:org/app.git")
		}
		return nil
	}
	if _, err := repoOnHost(row.BaseURL, repository); err != nil {
		return invalidf("%s", err.Error())
	}
	return nil
}

// Repos lists the repositories the connection can read whose name contains query.
func (s *Service) Repos(ctx context.Context, id, query string) ([]Repo, error) {
	row, c, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	var repos []Repo
	switch row.Type {
	case store.GitTypeGitLab:
		repos, err = s.gitlabRepos(ctx, row.BaseURL, c.Token, query)
	case store.GitTypeGitHubApp:
		repos, err = s.githubRepos(ctx, row.ID, c)
	case store.GitTypeDeployKey:
		return nil, invalidf("a deploy key cannot list repositories; type the repository's SSH URL")
	default:
		err = fmt.Errorf("unknown git connection type %q", row.Type)
	}
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := []Repo{}
	for _, r := range repos {
		if q == "" || strings.Contains(strings.ToLower(r.FullName), q) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].FullName) < strings.ToLower(out[j].FullName) })
	return out, nil
}

// Branches lists the branch names of one repository (by full name) of the connection.
func (s *Service) Branches(ctx context.Context, id, fullName string) ([]string, error) {
	if !fullNameRe.MatchString(fullName) || strings.Contains(fullName, "..") {
		return nil, invalidf("repo must be a repository name like org/app")
	}
	row, c, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	switch row.Type {
	case store.GitTypeGitLab:
		return s.gitlabBranches(ctx, row.BaseURL, c.Token, fullName)
	case store.GitTypeGitHubApp:
		return s.githubBranches(ctx, row.ID, c, fullName)
	case store.GitTypeDeployKey:
		return nil, invalidf("a deploy key cannot list branches; type the branch name")
	}
	return nil, fmt.Errorf("unknown git connection type %q", row.Type)
}

var fullNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)+$`)

// repoOnHost returns the full name ("org/app") of the repository at rawURL, which must
// be an https URL on the same host as baseURL.
func repoOnHost(baseURL, rawURL string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, base.Host) {
		return "", fmt.Errorf("the repository must be an https URL on %s to use this Git connection", base.Host)
	}
	name := strings.TrimSuffix(strings.Trim(strings.TrimPrefix(u.Path, base.Path), "/"), ".git")
	if !fullNameRe.MatchString(name) || strings.Contains(name, "..") {
		return "", errors.New("the repository URL must look like https://host/org/app.git")
	}
	return name, nil
}

// ---- HTTP ----

// apiError is a non-2xx answer of a Git host.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the Git host answered %d", e.Status)
	}
	return fmt.Sprintf("the Git host answered %d: %s", e.Status, e.Message)
}

// call sends a JSON request and decodes a 2xx answer into out (when not nil). It returns
// the response headers, for pagination.
func (s *Service) call(ctx context.Context, method, rawURL string, header map[string]string, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "panel")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := s.http.Do(req)
	if err != nil {
		// The URL may carry nothing secret, but the error text of net/http quotes it in full.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("could not reach %s: %w", req.URL.Host, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var m struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(body, &m)
		if m.Message == "" {
			m.Message = m.Error
		}
		return nil, &apiError{Status: res.StatusCode, Message: m.Message}
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return nil, fmt.Errorf("unexpected answer from %s: %w", req.URL.Host, err)
		}
	}
	return res.Header, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
