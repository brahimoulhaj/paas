package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Ref is a parsed image reference.
type Ref struct {
	Registry string // as written in the reference, e.g. "ghcr.io", "localhost:5000", "docker.io"
	Repo     string // e.g. "owner/berth", "library/nginx"
	Tag      string // "latest" when omitted
}

// Repository is the reference without its tag, as Docker writes it.
func (r Ref) Repository() string {
	if r.Registry == "docker.io" {
		return strings.TrimPrefix(r.Repo, "library/")
	}
	return r.Registry + "/" + r.Repo
}

func (r Ref) WithTag(tag string) string { return r.Repository() + ":" + tag }

// ParseRef splits an image reference. Digest references are refused: there is nothing
// newer than a pinned digest.
func ParseRef(ref string) (Ref, error) {
	if ref == "" || strings.Contains(ref, "@") {
		return Ref{}, fmt.Errorf("image %q: only tag references can be updated", ref)
	}
	name, tag := ref, "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		name, tag = ref[:i], ref[i+1:]
	}
	r := Ref{Registry: "docker.io", Repo: name, Tag: tag}
	if i := strings.Index(name, "/"); i > 0 {
		first := name[:i]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			r.Registry, r.Repo = first, name[i+1:]
		}
	}
	if r.Registry == "docker.io" && !strings.Contains(r.Repo, "/") {
		r.Repo = "library/" + r.Repo
	}
	if r.Repo == "" || tag == "" {
		return Ref{}, fmt.Errorf("image %q: invalid reference", ref)
	}
	return r, nil
}

// apiHost is where the registry API lives; Docker Hub serves it on its own host.
func (r Ref) apiHost() string {
	if r.Registry == "docker.io" {
		return "registry-1.docker.io"
	}
	return r.Registry
}

// Registry talks to the Docker Registry HTTP API v2 anonymously, which is enough for
// public images on GHCR, Docker Hub and self-hosted registries.
type Registry struct {
	HTTP *http.Client
	// Insecure reports whether a registry host may be reached over plain HTTP. The panel
	// asks the Docker daemon, which knows its configured insecure registries.
	Insecure func(host string) bool
}

// Tags lists the tags of the reference's repository.
func (g *Registry) Tags(ctx context.Context, r Ref) ([]string, error) {
	scheme := "https"
	if g.Insecure != nil && g.Insecure(r.Registry) {
		scheme = "http"
	}
	next := scheme + "://" + r.apiHost() + "/v2/" + r.Repo + "/tags/list?n=1000"
	var token string
	var tags []string
	for page := 0; next != "" && page < 20; page++ {
		res, err := g.get(ctx, next, token)
		if err != nil {
			return nil, err
		}
		if res.StatusCode == http.StatusUnauthorized && token == "" {
			challenge := res.Header.Get("WWW-Authenticate")
			res.Body.Close()
			if token, err = g.token(ctx, challenge, r.Repo); err != nil {
				return nil, err
			}
			if res, err = g.get(ctx, next, token); err != nil {
				return nil, err
			}
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			return nil, fmt.Errorf("list tags of %s: registry answered %s", r.Repository(), res.Status)
		}
		var body struct {
			Tags []string `json:"tags"`
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&body)
		res.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("list tags of %s: %w", r.Repository(), err)
		}
		tags = append(tags, body.Tags...)
		next = nextPage(next, res.Header.Get("Link"))
	}
	return tags, nil
}

func (g *Registry) get(ctx context.Context, u, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	c := g.HTTP
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	return c.Do(req)
}

var challengeParam = regexp.MustCompile(`(\w+)="([^"]*)"`)

// token follows a Bearer challenge to get an anonymous pull token.
func (g *Registry) token(ctx context.Context, challenge, repo string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", errors.New("registry wants credentials; only public images can be checked")
	}
	params := map[string]string{}
	for _, m := range challengeParam.FindAllStringSubmatch(challenge, -1) {
		params[strings.ToLower(m[1])] = m[2]
	}
	realm, err := url.Parse(params["realm"])
	if err != nil || realm.Host == "" {
		return "", fmt.Errorf("registry token challenge has no usable realm: %q", challenge)
	}
	q := realm.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	q.Set("scope", "repository:"+repo+":pull")
	realm.RawQuery = q.Encode()
	res, err := g.get(ctx, realm.String(), "")
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry token: %s (is the image public?)", res.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("registry token: %w", err)
	}
	if body.Token == "" {
		body.Token = body.AccessToken
	}
	return body.Token, nil
}

// nextPage resolves an RFC 5988 Link header with rel="next" against the current URL.
func nextPage(current, link string) string {
	if !strings.Contains(link, `rel="next"`) {
		return ""
	}
	start, end := strings.Index(link, "<"), strings.Index(link, ">")
	if start < 0 || end < start {
		return ""
	}
	base, err := url.Parse(current)
	if err != nil {
		return ""
	}
	u, err := base.Parse(link[start+1 : end])
	if err != nil {
		return ""
	}
	return u.String()
}

// Version is a parsed semantic version tag ("1.2.3", "v1.2.3", "1.2.3-rc.1").
type Version struct {
	Major, Minor, Patch int
	Pre                 string
	Tag                 string
}

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)

func ParseVersion(tag string) (Version, bool) {
	m := semverRe.FindStringSubmatch(tag)
	if m == nil {
		return Version{}, false
	}
	v := Version{Pre: m[4], Tag: tag}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	return v, true
}

// Less orders versions; a pre-release sorts before its release.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	if v.Patch != o.Patch {
		return v.Patch < o.Patch
	}
	if v.Pre == o.Pre {
		return false
	}
	if v.Pre == "" || o.Pre == "" {
		return v.Pre != "" // only v is a pre-release
	}
	return comparePre(v.Pre, o.Pre) < 0
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		ai, aErr := strconv.Atoi(as[i])
		bi, bErr := strconv.Atoi(bs[i])
		switch {
		case aErr == nil && bErr == nil && ai != bi:
			if ai < bi {
				return -1
			}
			return 1
		case aErr == nil && bErr != nil:
			return -1 // numeric identifiers sort first
		case aErr != nil && bErr == nil:
			return 1
		case as[i] != bs[i]:
			return strings.Compare(as[i], bs[i])
		}
	}
	return len(as) - len(bs)
}

// Newest returns the highest version tag above current. Pre-releases are only offered to
// a panel that already runs one.
func Newest(current Version, tags []string) (Version, bool) {
	best, found := current, false
	for _, t := range tags {
		v, ok := ParseVersion(t)
		if !ok || (v.Pre != "" && current.Pre == "") {
			continue
		}
		if best.Less(v) {
			best, found = v, true
		}
	}
	return best, found
}
