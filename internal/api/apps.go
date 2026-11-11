package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"panel/internal/deploy"
	"panel/internal/executor"
	"panel/internal/proxy"
	"panel/internal/store"
)

// Deployer runs apps; *deploy.Service implements it.
type Deployer interface {
	Deploy(ctx context.Context, appID, trigger string) (store.Deployment, error)
	Start(ctx context.Context, appID string) error
	Stop(ctx context.Context, appID string) error
	Restart(ctx context.Context, appID string) error
	Remove(ctx context.Context, appID string) error
	RuntimeState(ctx context.Context, appID string) string
	LogPath(dep store.Deployment) string
	// StreamLogs calls fn for each line of the app's current container until the stream
	// ends or ctx is cancelled.
	StreamLogs(ctx context.Context, appID string, opts executor.LogOptions, fn func(executor.LogLine)) error
}

// SetDeployer connects the deploy service; without one, runtime actions answer 503.
func (s *Server) SetDeployer(d Deployer) { s.deployer = d }

var (
	appNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)
	imageRe   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]{0,254}$`)
)

// ---- JSON shapes ----

type projectJSON struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	AppCount    int       `json:"app_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toProjectJSON(p store.Project) projectJSON {
	return projectJSON{p.ID, p.Name, p.Description, p.AppCount, p.CreatedAt, p.UpdatedAt}
}

type appJSON struct {
	ID                  string    `json:"id"`
	ProjectID           string    `json:"project_id"`
	Name                string    `json:"name"`
	SourceType          string    `json:"source_type"`
	Image               string    `json:"image"`
	Port                int       `json:"port"`
	CPULimit            float64   `json:"cpu_limit"`
	MemoryLimitMB       int64     `json:"memory_limit_mb"`
	DesiredState        string    `json:"desired_state"`
	CurrentDeploymentID string    `json:"current_deployment_id"`
	RuntimeState        string    `json:"runtime_state,omitempty"` // single-app responses only
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func toAppJSON(a store.App) appJSON {
	return appJSON{
		ID: a.ID, ProjectID: a.ProjectID, Name: a.Name, SourceType: a.SourceType, Image: a.Image, Port: a.Port,
		CPULimit: a.CPULimit, MemoryLimitMB: a.MemoryMB, DesiredState: a.DesiredState,
		CurrentDeploymentID: a.CurrentDeploymentID, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
}

type deploymentJSON struct {
	ID            string     `json:"id"`
	AppID         string     `json:"app_id"`
	Status        string     `json:"status"`
	Trigger       string     `json:"trigger"`
	Image         string     `json:"image"`
	Port          int        `json:"port"`
	ContainerName string     `json:"container_name"`
	Error         string     `json:"error"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

func toDeploymentJSON(d store.Deployment) deploymentJSON {
	out := deploymentJSON{
		ID: d.ID, AppID: d.AppID, Status: d.Status, Trigger: d.Trigger, Image: d.Image, Port: d.Port,
		ContainerName: d.ContainerName, Error: d.Error, CreatedAt: d.CreatedAt,
	}
	if !d.StartedAt.IsZero() {
		out.StartedAt = &d.StartedAt
	}
	if !d.FinishedAt.IsZero() {
		out.FinishedAt = &d.FinishedAt
	}
	return out
}

type domainJSON struct {
	ID        string    `json:"id"`
	AppID     string    `json:"app_id"`
	Hostname  string    `json:"hostname"`
	TLSMode   string    `json:"tls_mode"`
	CreatedAt time.Time `json:"created_at"`
}

func toDomainJSON(d store.Domain) domainJSON {
	return domainJSON{d.ID, d.AppID, d.Hostname, d.TLSMode, d.CreatedAt}
}

type eventJSON struct {
	ID         string    `json:"id"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	TargetName string    `json:"target_name"`
	Detail     string    `json:"detail"`
	CreatedAt  time.Time `json:"created_at"`
}

func mapSlice[T, U any](in []T, f func(T) U) []U {
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

// ---- shared helpers ----

// storeError answers the usual store failures and reports whether it wrote a response.
func (s *Server) storeError(w http.ResponseWriter, what string, err error, conflictMsg string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", conflictMsg)
	default:
		s.internal(w, what, err)
	}
	return true
}

// audit records a mutating action. A failed audit write is logged, never returned: the
// action itself has already happened.
func (s *Server) audit(r *http.Request, action, targetType, targetID, targetName, detail string) {
	u := userFrom(r.Context())
	actor := u.Email
	if t := tokenFrom(r.Context()); t != nil {
		actor += " (token " + t.Name + ")"
	}
	err := s.store.AddEvent(r.Context(), store.Event{
		UserID: u.ID, Actor: actor, Action: action,
		TargetType: targetType, TargetID: targetID, TargetName: targetName, Detail: detail,
	})
	if err != nil {
		s.log.Error("record event", "action", action, "error", err)
	}
}

// applyProxy pushes the routing state to the proxy and reports whether it is live.
func (s *Server) applyProxy(ctx context.Context) bool {
	if s.proxy == nil {
		return true
	}
	if err := s.proxy.Apply(ctx); err != nil {
		s.log.Error("apply proxy config", "error", err)
		return false
	}
	return true
}

func invalid(w http.ResponseWriter, msg string) {
	writeError(w, http.StatusUnprocessableEntity, "invalid", msg)
}

// ---- projects ----

func validProject(w http.ResponseWriter, name, description string) bool {
	if name == "" || len(name) > 64 {
		invalid(w, "name must be 1 to 64 characters")
		return false
	}
	if len(description) > 500 {
		invalid(w, "description must be at most 500 characters")
		return false
	}
	return true
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListProjects(r.Context())
	if err != nil {
		s.internal(w, "list projects", err)
		return
	}
	writeJSON(w, http.StatusOK, mapSlice(list, toProjectJSON))
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if !validProject(w, in.Name, in.Description) {
		return
	}
	p, err := s.store.CreateProject(r.Context(), in.Name, in.Description)
	if s.storeError(w, "create project", err, "a project with this name already exists") {
		return
	}
	s.audit(r, "project.create", "project", p.ID, p.Name, "")
	writeJSON(w, http.StatusCreated, toProjectJSON(p))
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetProject(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get project", err, "") {
		return
	}
	writeJSON(w, http.StatusOK, toProjectJSON(p))
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	p, err := s.store.GetProject(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get project", err, "") {
		return
	}
	if in.Name != nil {
		p.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		p.Description = strings.TrimSpace(*in.Description)
	}
	if !validProject(w, p.Name, p.Description) {
		return
	}
	p, err = s.store.UpdateProject(r.Context(), p.ID, p.Name, p.Description)
	if s.storeError(w, "update project", err, "a project with this name already exists") {
		return
	}
	s.audit(r, "project.update", "project", p.ID, p.Name, "")
	writeJSON(w, http.StatusOK, toProjectJSON(p))
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetProject(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get project", err, "") {
		return
	}
	err = s.store.DeleteProject(r.Context(), p.ID)
	if s.storeError(w, "delete project", err, "delete the project's apps first") {
		return
	}
	s.audit(r, "project.delete", "project", p.ID, p.Name, "")
	w.WriteHeader(http.StatusNoContent)
}

// ---- apps ----

func validApp(w http.ResponseWriter, in store.AppInput) bool {
	switch {
	case !appNameRe.MatchString(in.Name):
		invalid(w, "name must be 1 to 40 lowercase letters, digits or dashes, and not start or end with a dash")
	case !imageRe.MatchString(in.Image) || strings.Contains(in.Image, ".."):
		invalid(w, "image must be an image reference like nginx:1.27 or ghcr.io/org/app:tag")
	case in.Port < 1 || in.Port > 65535:
		invalid(w, "port must be between 1 and 65535")
	case in.CPULimit < 0 || in.CPULimit > 256:
		invalid(w, "cpu_limit must be between 0 (unlimited) and 256 cores")
	case in.MemoryMB < 0 || (in.MemoryMB > 0 && in.MemoryMB < 6):
		invalid(w, "memory_limit_mb must be 0 (unlimited) or at least 6")
	default:
		return true
	}
	return false
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetProject(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get project", err, "") {
		return
	}
	list, err := s.store.ListApps(r.Context(), p.ID)
	if err != nil {
		s.internal(w, "list apps", err)
		return
	}
	writeJSON(w, http.StatusOK, mapSlice(list, toAppJSON))
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          string  `json:"name"`
		Image         string  `json:"image"`
		Port          int     `json:"port"`
		CPULimit      float64 `json:"cpu_limit"`
		MemoryLimitMB int64   `json:"memory_limit_mb"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	input := store.AppInput{
		Name: strings.TrimSpace(in.Name), Image: strings.TrimSpace(in.Image), Port: in.Port,
		CPULimit: in.CPULimit, MemoryMB: in.MemoryLimitMB,
	}
	if !validApp(w, input) {
		return
	}
	a, err := s.store.CreateApp(r.Context(), r.PathValue("id"), input)
	if s.storeError(w, "create app", err, "an app with this name already exists in the project") {
		return
	}
	s.audit(r, "app.create", "app", a.ID, a.Name, a.Image)
	writeJSON(w, http.StatusCreated, toAppJSON(a))
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get app", err, "") {
		return
	}
	out := toAppJSON(a)
	if s.deployer != nil {
		out.RuntimeState = s.deployer.RuntimeState(r.Context(), a.ID)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUpdateApp changes settings only. Image, port and limits take effect with the next
// deploy; the running container is left alone.
func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          *string  `json:"name"`
		Image         *string  `json:"image"`
		Port          *int     `json:"port"`
		CPULimit      *float64 `json:"cpu_limit"`
		MemoryLimitMB *int64   `json:"memory_limit_mb"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get app", err, "") {
		return
	}
	input := store.AppInput{Name: a.Name, Image: a.Image, Port: a.Port, CPULimit: a.CPULimit, MemoryMB: a.MemoryMB}
	if in.Name != nil {
		input.Name = strings.TrimSpace(*in.Name)
	}
	if in.Image != nil {
		input.Image = strings.TrimSpace(*in.Image)
	}
	if in.Port != nil {
		input.Port = *in.Port
	}
	if in.CPULimit != nil {
		input.CPULimit = *in.CPULimit
	}
	if in.MemoryLimitMB != nil {
		input.MemoryMB = *in.MemoryLimitMB
	}
	if !validApp(w, input) {
		return
	}
	a, err = s.store.UpdateApp(r.Context(), a.ID, input)
	if s.storeError(w, "update app", err, "an app with this name already exists in the project") {
		return
	}
	s.audit(r, "app.update", "app", a.ID, a.Name, "")
	writeJSON(w, http.StatusOK, toAppJSON(a))
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get app", err, "") {
		return
	}
	if s.deployer != nil {
		// Containers first: if this fails the app is still there to retry on.
		if s.deployError(w, "remove app containers", s.deployer.Remove(r.Context(), a.ID)) {
			return
		}
	}
	if s.storeError(w, "delete app", s.store.DeleteApp(r.Context(), a.ID), "") {
		return
	}
	s.audit(r, "app.delete", "app", a.ID, a.Name, "")
	s.applyProxy(r.Context()) // drops the app's routes; a failure is retried by the proxy supervisor
	w.WriteHeader(http.StatusNoContent)
}

// ---- runtime: deploy, start, stop, restart ----

// deployError answers failures of the deploy service and reports whether it wrote a response.
func (s *Server) deployError(w http.ResponseWriter, what string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, deploy.ErrBusy):
		writeError(w, http.StatusConflict, "busy", err.Error())
	case errors.Is(err, deploy.ErrNotDeployed):
		writeError(w, http.StatusConflict, "not_deployed", "deploy the app first")
	default:
		s.log.Error(what, "error", err)
		writeError(w, http.StatusBadGateway, "runtime_error", "the container host could not complete the action; see the panel log")
	}
	return true
}

func (s *Server) requireDeployer(w http.ResponseWriter) bool {
	if s.deployer == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the deploy service is not running")
		return false
	}
	return true
}

// handleDeploy starts a deployment and answers 202 at once; the client polls the
// deployment for its outcome.
func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	if !s.requireDeployer(w) {
		return
	}
	a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get app", err, "") {
		return
	}
	dep, err := s.deployer.Deploy(r.Context(), a.ID, "manual")
	if s.deployError(w, "deploy", err) {
		return
	}
	s.audit(r, "app.deploy", "app", a.ID, a.Name, dep.ID)
	writeJSON(w, http.StatusAccepted, toDeploymentJSON(dep))
}

func (s *Server) handleAppAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireDeployer(w) {
			return
		}
		a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
		if s.storeError(w, "get app", err, "") {
			return
		}
		switch action {
		case "start":
			err = s.deployer.Start(r.Context(), a.ID)
		case "stop":
			err = s.deployer.Stop(r.Context(), a.ID)
		case "restart":
			err = s.deployer.Restart(r.Context(), a.ID)
		}
		if s.deployError(w, action+" app", err) {
			return
		}
		s.audit(r, "app."+action, "app", a.ID, a.Name, "")
		a, err = s.store.GetApp(r.Context(), a.ID)
		if s.storeError(w, "get app", err, "") {
			return
		}
		out := toAppJSON(a)
		out.RuntimeState = s.deployer.RuntimeState(r.Context(), a.ID)
		writeJSON(w, http.StatusOK, out)
	}
}

// ---- deployments ----

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get app", err, "") {
		return
	}
	list, err := s.store.ListDeployments(r.Context(), a.ID, 50)
	if err != nil {
		s.internal(w, "list deployments", err)
		return
	}
	writeJSON(w, http.StatusOK, mapSlice(list, toDeploymentJSON))
}

func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.GetDeployment(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get deployment", err, "") {
		return
	}
	writeJSON(w, http.StatusOK, toDeploymentJSON(d))
}

// handleDeploymentLog returns the log so far as plain text. Live streaming comes with SSE.
func (s *Server) handleDeploymentLog(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.GetDeployment(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get deployment", err, "") {
		return
	}
	var body []byte
	if s.deployer != nil && d.LogPath != "" {
		body, err = os.ReadFile(s.deployer.LogPath(d))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			s.internal(w, "read deployment log", err)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// ---- domains ----

func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get app", err, "") {
		return
	}
	list, err := s.store.ListDomains(r.Context(), a.ID)
	if err != nil {
		s.internal(w, "list domains", err)
		return
	}
	writeJSON(w, http.StatusOK, mapSlice(list, toDomainJSON))
}

func (s *Server) handleCreateDomain(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hostname string `json:"hostname"`
		TLSMode  string `json:"tls_mode"` // optional; "auto" (default) or "http_only"
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.TLSMode == "" {
		in.TLSMode = store.TLSAuto
	}
	if !store.ValidTLSMode(in.TLSMode) {
		invalid(w, `tls_mode must be "auto" or "http_only"`)
		return
	}
	host := strings.ToLower(strings.TrimSpace(in.Hostname))
	if err := proxy.ValidHost(host); err != nil {
		invalid(w, "hostname must be a domain name like app.example.com (no scheme, port, path or wildcard)")
		return
	}
	a, err := s.store.GetApp(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get app", err, "") {
		return
	}
	panelDomain, _, err := s.store.GetSetting(r.Context(), store.SettingPanelDomain)
	if err != nil {
		s.internal(w, "create domain", err)
		return
	}
	if host == panelDomain {
		writeError(w, http.StatusConflict, "conflict", "this hostname is the panel's own domain")
		return
	}
	d, err := s.store.CreateDomain(r.Context(), a.ID, host, in.TLSMode)
	if s.storeError(w, "create domain", err, "this hostname is already used by an app") {
		return
	}
	s.audit(r, "domain.add", "app", a.ID, a.Name, host)
	if !s.applyProxy(r.Context()) {
		writeError(w, http.StatusServiceUnavailable, "proxy_unavailable",
			"saved, but the proxy could not be updated; it is applied when the proxy is back")
		return
	}
	writeJSON(w, http.StatusCreated, toDomainJSON(d))
}

// handleUpdateDomain changes how a domain is served: "auto" (HTTPS with a certificate,
// HTTP redirected) or "http_only" (plain HTTP, for a CDN that terminates TLS itself).
func (s *Server) handleUpdateDomain(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TLSMode string `json:"tls_mode"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !store.ValidTLSMode(in.TLSMode) {
		invalid(w, `tls_mode must be "auto" or "http_only"`)
		return
	}
	d, err := s.store.GetDomain(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get domain", err, "") {
		return
	}
	if d.TLSMode != in.TLSMode {
		if s.storeError(w, "update domain", s.store.SetDomainTLSMode(r.Context(), d.ID, in.TLSMode), "") {
			return
		}
		appName := ""
		if a, err := s.store.GetApp(r.Context(), d.AppID); err == nil {
			appName = a.Name
		}
		s.audit(r, "domain.tls", "app", d.AppID, appName, d.Hostname+": "+in.TLSMode)
		if !s.applyProxy(r.Context()) {
			writeError(w, http.StatusServiceUnavailable, "proxy_unavailable",
				"saved, but the proxy could not be updated; it is applied when the proxy is back")
			return
		}
		d.TLSMode = in.TLSMode
	}
	writeJSON(w, http.StatusOK, toDomainJSON(d))
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.GetDomain(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get domain", err, "") {
		return
	}
	if s.storeError(w, "delete domain", s.store.DeleteDomain(r.Context(), d.ID), "") {
		return
	}
	appName := ""
	if a, err := s.store.GetApp(r.Context(), d.AppID); err == nil {
		appName = a.Name
	}
	s.audit(r, "domain.remove", "app", d.AppID, appName, d.Hostname)
	s.applyProxy(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// handleDomainDNS is a hint, not a gate: it shows what the hostname resolves to and, when
// this server has public addresses, whether one of them is among them. Behind NAT or a
// CDN the answer is "unknown" (null), and the domain can still work.
func (s *Server) handleDomainDNS(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.GetDomain(r.Context(), r.PathValue("id"))
	if s.storeError(w, "get domain", err, "") {
		return
	}
	out := s.dnsHint(r.Context(), d.Hostname)
	writeJSON(w, http.StatusOK, out)
}

// dnsHint looks a hostname up and compares the answer with this server's public addresses.
func (s *Server) dnsHint(ctx context.Context, hostname string) map[string]any {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	addrs, lookupErr := s.lookupHost(ctx, hostname)
	if addrs == nil {
		addrs = []string{}
	}
	server := s.serverAddrs()
	out := map[string]any{
		"hostname":         hostname,
		"resolves":         lookupErr == nil && len(addrs) > 0,
		"addresses":        addrs,
		"server_addresses": server,
		"points_here":      nil,
	}
	if len(server) > 0 {
		match := false
		for _, a := range addrs {
			for _, sa := range server {
				match = match || a == sa
			}
		}
		out["points_here"] = match
	}
	return out
}

// publicAddrs lists this machine's public unicast addresses.
func publicAddrs() []string {
	out := []string{}
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range ifaces {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(ipnet.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
			out = append(out, ip.String())
		}
	}
	return out
}

// ---- events ----

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			invalid(w, "limit must be between 1 and 200")
			return
		}
		limit = n
	}
	list, err := s.store.ListEvents(r.Context(), limit)
	if err != nil {
		s.internal(w, "list events", err)
		return
	}
	writeJSON(w, http.StatusOK, mapSlice(list, func(e store.Event) eventJSON {
		return eventJSON{e.ID, e.Actor, e.Action, e.TargetType, e.TargetID, e.TargetName, e.Detail, e.CreatedAt}
	}))
}
