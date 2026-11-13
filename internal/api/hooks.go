package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"panel/internal/store"
)

// tagRe is an image tag, as Docker accepts it.
var tagRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

type deployHookJSON struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token,omitempty"` // the URL is /api/hooks/deploy/<token>
}

// handleGetDeployHook tells whether the app's deploy webhook is on, with its token.
func (s *Server) handleGetDeployHook(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get app", err, "") {
		return
	}
	token, err := s.store.AppDeployHook(r.Context(), a.ID)
	if err != nil {
		s.internal(w, "deploy hook", err)
		return
	}
	writeJSON(w, http.StatusOK, deployHookJSON{Enabled: token != "", Token: token})
}

// handleNewDeployHook turns the webhook on with a new token; an old URL stops working.
func (s *Server) handleNewDeployHook(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get app", err, "") {
		return
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		s.internal(w, "deploy hook", err)
		return
	}
	token := hex.EncodeToString(b)
	if err := s.store.SetAppDeployHook(r.Context(), a.ID, token); err != nil {
		s.internal(w, "deploy hook", err)
		return
	}
	detail := "on"
	if a.DeployHook {
		detail = "new URL"
	}
	s.audit(r, "app.deploy_hook", "app", a.ID, a.Name, detail)
	writeJSON(w, http.StatusOK, deployHookJSON{Enabled: true, Token: token})
}

func (s *Server) handleDeleteDeployHook(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get app", err, "") {
		return
	}
	if err := s.store.SetAppDeployHook(r.Context(), a.ID, ""); err != nil {
		s.internal(w, "deploy hook", err)
		return
	}
	s.audit(r, "app.deploy_hook", "app", a.ID, a.Name, "off")
	writeJSON(w, http.StatusOK, deployHookJSON{})
}

// hookEvent is what a webhook request asks for. Git hosts send a push (with the branch
// in ref); a CI script calls with no event, optionally with an image tag.
type hookEvent struct {
	kind   string // "push", "ping", "call", or the host's own event name (ignored)
	branch string // push: the branch pushed; "" for a tag push
	delete bool   // push: the branch was deleted
	tag    string // call: deploy this image tag
}

func readHookEvent(r *http.Request) (hookEvent, error) {
	event := strings.ToLower(firstHeader(r, "X-GitHub-Event", "X-Gitea-Event", "X-Forgejo-Event", "X-Gogs-Event", "X-Gitlab-Event"))
	var body struct {
		Ref     string `json:"ref"`
		Deleted bool   `json:"deleted"`
		After   string `json:"after"`
		Tag     string `json:"tag"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 25<<20)) // GitHub caps push payloads at 25 MB
	if err != nil {
		return hookEvent{}, err
	}
	// GitHub's default content type sends the JSON form-encoded, in a payload field.
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		form, err := url.ParseQuery(string(raw))
		if err != nil {
			return hookEvent{}, errors.New("the form body could not be read")
		}
		raw = []byte(form.Get("payload"))
	}
	if len(strings.TrimSpace(string(raw))) > 0 {
		// Git hosts send JSON (GitHub only when the webhook's content type is application/json).
		if err := json.Unmarshal(raw, &body); err != nil {
			return hookEvent{}, errors.New("the body must be JSON")
		}
	}
	if tag := r.URL.Query().Get("tag"); tag != "" {
		body.Tag = tag
	}
	push := func() hookEvent {
		e := hookEvent{kind: "push", delete: body.Deleted || strings.Trim(body.After, "0") == "" && body.After != ""}
		if b, ok := strings.CutPrefix(body.Ref, "refs/heads/"); ok {
			e.branch = b
		}
		return e
	}
	switch event {
	case "push", "push hook":
		return push(), nil
	case "ping":
		return hookEvent{kind: "ping"}, nil
	case "":
		if body.Ref != "" { // a host that names no event but sends a push payload
			return push(), nil
		}
		return hookEvent{kind: "call", tag: body.Tag}, nil
	}
	return hookEvent{kind: event}, nil
}

func firstHeader(r *http.Request, names ...string) string {
	for _, n := range names {
		if v := r.Header.Get(n); v != "" {
			return v
		}
	}
	return ""
}

// handleDeployHook is POST /api/hooks/deploy/<token>: the token in the URL is the only
// credential. A push to the app's branch deploys it; pushes to other branches, deleted
// branches and tags are ignored, with the reason in the answer. A call without a Git
// event deploys; an image app can name a new tag. A deploy arriving while the app is busy
// runs once the current one ends.
func (s *Server) handleDeployHook(w http.ResponseWriter, r *http.Request) {
	if !s.requireDeployer(w) {
		return
	}
	a, err := s.store.AppByDeployHook(r.Context(), r.PathValue("token"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no app has this deploy webhook; it may have been turned off or replaced")
		return
	}
	if err != nil {
		s.internal(w, "deploy hook", err)
		return
	}
	ev, err := readHookEvent(r)
	if err != nil {
		invalid(w, err.Error())
		return
	}
	ignore := func(reason string) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ignored", "reason": reason})
	}
	trigger := "webhook"
	switch ev.kind {
	case "ping":
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "app": a.Name})
		return
	case "push":
		switch {
		case a.SourceType != store.SourceGit:
			ignore("the app deploys an image, not a Git branch; call the webhook from CI instead")
			return
		case ev.branch == "":
			ignore("not a branch push (a tag?)")
			return
		case ev.delete:
			ignore("the branch was deleted")
			return
		case ev.branch != a.Git.Branch:
			ignore("push to " + ev.branch + "; the app deploys " + a.Git.Branch)
			return
		}
		trigger = "push"
	case "call":
		if ev.tag != "" {
			if a.SourceType != store.SourceImage {
				invalid(w, "tag is for image apps; a Git app deploys its branch")
				return
			}
			if !tagRe.MatchString(ev.tag) {
				invalid(w, "tag must be an image tag like 1.4.2 or sha-3f2a9c1")
				return
			}
			image := withTag(a.Image, ev.tag)
			if image != a.Image {
				if err := s.store.SetAppImage(r.Context(), a.ID, image); err != nil {
					s.internal(w, "deploy hook", err)
					return
				}
				a.Image = image
			}
		}
	default:
		ignore(ev.kind + " events do not deploy; send push events")
		return
	}

	dep, queued, err := s.deployer.DeployOrQueue(r.Context(), a.ID, trigger)
	if s.deployError(w, "deploy hook", err) {
		return
	}
	detail := trigger
	if ev.tag != "" {
		detail += " " + a.Image
	}
	if err := s.store.AddEvent(r.Context(), store.Event{Actor: "webhook", Action: "app.deploy", TargetType: "app", TargetID: a.ID, TargetName: a.Name, Detail: detail}); err != nil {
		s.log.Error("record event", "error", err)
	}
	if queued {
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "reason": "a deployment is running; this one starts when it ends"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "deploying", "deployment": toDeploymentJSON(dep)})
}

// withTag replaces the tag (or digest) of an image reference: "ghcr.io/org/app:1" with
// tag 2 is "ghcr.io/org/app:2". A colon before the last slash is a registry port.
func withTag(image, tag string) string {
	repo := image
	if i := strings.Index(repo, "@"); i >= 0 {
		repo = repo[:i]
	}
	if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}
	return repo + ":" + tag
}
