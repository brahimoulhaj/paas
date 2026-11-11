package gitsource

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"panel/internal/store"
)

// AddGitLab stores an access token for gitlab.com or a self-hosted GitLab, after checking
// that it can list projects. The token needs the read_api and read_repository scopes.
func (s *Service) AddGitLab(ctx context.Context, name, baseURL, token string) (Connection, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return Connection{}, invalidf("base_url must be the https address of a GitLab, like https://gitlab.com")
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 500 || strings.ContainsAny(token, " \t\r\n") {
		return Connection{}, invalidf("token must be a GitLab access token")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = u.Host
	}
	if len(name) > 64 {
		return Connection{}, invalidf("name must be at most 64 characters")
	}
	if _, err := s.gitlabRepos(ctx, baseURL, token, ""); err != nil {
		var ae *apiError
		if errors.As(err, &ae) && (ae.Status == 401 || ae.Status == 403) {
			return Connection{}, invalidf("GitLab refused the token (%d). It needs the read_api and read_repository scopes", ae.Status)
		}
		return Connection{}, invalidf("%s", err.Error())
	}
	return s.save(ctx, store.GitTypeGitLab, name, baseURL, creds{Token: token})
}

func gitlabAuth(token string) map[string]string { return map[string]string{"PRIVATE-TOKEN": token} }

// gitlabRepos returns up to 100 projects the token is a member of, most recently active
// first; query narrows the search on GitLab's side.
func (s *Service) gitlabRepos(ctx context.Context, baseURL, token, query string) ([]Repo, error) {
	q := url.Values{
		"membership": {"true"}, "simple": {"true"}, "archived": {"false"},
		"order_by": {"last_activity_at"}, "per_page": {"100"},
	}
	if query = strings.TrimSpace(query); query != "" {
		q.Set("search", query)
		q.Set("search_namespaces", "true")
	}
	var projects []struct {
		Path          string `json:"path_with_namespace"`
		HTTPURL       string `json:"http_url_to_repo"`
		DefaultBranch string `json:"default_branch"`
		Visibility    string `json:"visibility"`
	}
	if _, err := s.call(ctx, "GET", baseURL+"/api/v4/projects?"+q.Encode(), gitlabAuth(token), &projects); err != nil {
		return nil, err
	}
	out := make([]Repo, 0, len(projects))
	for _, p := range projects {
		out = append(out, Repo{FullName: p.Path, CloneURL: p.HTTPURL, DefaultBranch: p.DefaultBranch, Private: p.Visibility != "public"})
	}
	return out, nil
}

func (s *Service) gitlabBranches(ctx context.Context, baseURL, token, fullName string) ([]string, error) {
	var branches []struct {
		Name string `json:"name"`
	}
	u := baseURL + "/api/v4/projects/" + url.PathEscape(fullName) + "/repository/branches?per_page=100"
	if _, err := s.call(ctx, "GET", u, gitlabAuth(token), &branches); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(branches))
	for _, b := range branches {
		out = append(out, b.Name)
	}
	return out, nil
}
