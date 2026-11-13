// Package api is the panel's HTTP surface: the JSON API under /api and the embedded web app.
package api

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"panel/internal/account"
	"panel/internal/brand"
	"panel/internal/gitsource"
	"panel/internal/store"
)

type Server struct {
	store         *store.Store
	accounts      *account.Service
	log           *slog.Logger
	version       string
	web           fs.FS
	authRate      *rateLimiter
	proxy         ProxyApplier
	deployer      Deployer
	updater       Updater
	git           *gitsource.Service
	metrics       HostMetrics
	dockerMetrics DockerMetrics
	buildDefaults store.BuildLimits

	logPoll  time.Duration // how often a deployment log stream looks for new lines
	stop     chan struct{} // closed when the server shuts down; ends open streams
	stopOnce sync.Once

	// Seams for tests.
	lookupHost  func(ctx context.Context, host string) ([]string, error)
	serverAddrs func() []string
}

func New(st *store.Store, accounts *account.Service, log *slog.Logger, version string, web fs.FS) *Server {
	return &Server{
		store:    st,
		accounts: accounts,
		log:      log,
		version:  version,
		web:      web,
		authRate: newRateLimiter(10, 5*time.Minute),

		logPoll: 300 * time.Millisecond,
		stop:    make(chan struct{}),

		lookupHost:  net.DefaultResolver.LookupHost,
		serverAddrs: publicAddrs,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/meta", s.handleMeta)

	mux.HandleFunc("POST /api/setup", s.limit(s.authRate, s.handleSetup))
	mux.HandleFunc("POST /api/auth/login", s.limit(s.authRate, s.handleLogin))
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))

	mux.HandleFunc("GET /api/settings/panel", s.requireAuth(s.handleGetPanelSettings))
	mux.HandleFunc("PUT /api/settings/panel", s.requireAuth(s.handlePutPanelSettings))
	mux.HandleFunc("GET /api/settings/panel/dns", s.requireAuth(s.handlePanelDNS))

	mux.HandleFunc("GET /api/settings/builds", s.requireAuth(s.handleGetBuildSettings))
	mux.HandleFunc("PUT /api/settings/builds", s.requireAuth(s.handlePutBuildSettings))
	mux.HandleFunc("DELETE /api/settings/builds", s.requireAuth(s.handleResetBuildSettings))

	mux.HandleFunc("GET /api/onboarding", s.requireAuth(s.handleGetOnboarding))
	mux.HandleFunc("POST /api/onboarding/complete", s.requireAuth(s.handleCompleteOnboarding))

	mux.HandleFunc("GET /api/updates", s.requireAuth(s.handleGetUpdates))
	mux.HandleFunc("POST /api/updates/check", s.requireAuth(s.handleCheckUpdates))
	mux.HandleFunc("POST /api/updates/apply", s.requireAuth(s.handleApplyUpdate))
	mux.HandleFunc("PUT /api/updates/settings", s.requireAuth(s.handleUpdateSettings))

	mux.HandleFunc("GET /api/tokens", s.requireAuth(sessionOnly(s.handleListTokens)))
	mux.HandleFunc("POST /api/tokens", s.requireAuth(sessionOnly(s.handleCreateToken)))
	mux.HandleFunc("DELETE /api/tokens/{id}", s.requireAuth(sessionOnly(s.handleDeleteToken)))

	mux.HandleFunc("GET /api/git-sources", s.requireAuth(s.withGit(s.handleListGitSources)))
	mux.HandleFunc("POST /api/git-sources/gitlab", s.requireAuth(s.withGit(s.handleAddGitLab)))
	mux.HandleFunc("POST /api/git-sources/github", s.requireAuth(s.withGit(s.handleStartGitHub)))
	mux.HandleFunc("GET "+gitsource.CallbackPath, s.requireAuth(s.withGit(s.handleGitHubCallback)))
	mux.HandleFunc("DELETE /api/git-sources/{id}", s.requireAuth(s.withGit(s.handleDeleteGitSource)))
	mux.HandleFunc("GET /api/git-sources/{id}/repos", s.requireAuth(s.withGit(s.handleGitSourceRepos)))
	mux.HandleFunc("GET /api/git-sources/{id}/branches", s.requireAuth(s.withGit(s.handleGitSourceBranches)))

	mux.HandleFunc("GET /api/projects", s.requireAuth(s.handleListProjects))
	mux.HandleFunc("POST /api/projects", s.requireAuth(s.handleCreateProject))
	mux.HandleFunc("GET /api/projects/{id}", s.requireAuth(s.handleGetProject))
	mux.HandleFunc("PATCH /api/projects/{id}", s.requireAuth(s.handleUpdateProject))
	mux.HandleFunc("DELETE /api/projects/{id}", s.requireAuth(s.handleDeleteProject))
	mux.HandleFunc("GET /api/projects/{id}/apps", s.requireAuth(s.handleListApps))
	mux.HandleFunc("POST /api/projects/{id}/apps", s.requireAuth(s.handleCreateApp))

	mux.HandleFunc("GET /api/apps/{id}", s.requireAuth(s.handleGetApp))
	mux.HandleFunc("PATCH /api/apps/{id}", s.requireAuth(s.handleUpdateApp))
	mux.HandleFunc("DELETE /api/apps/{id}", s.requireAuth(s.handleDeleteApp))
	mux.HandleFunc("POST /api/apps/{id}/deploy", s.requireAuth(s.handleDeploy))
	mux.HandleFunc("POST /api/apps/{id}/start", s.requireAuth(s.handleAppAction("start")))
	mux.HandleFunc("POST /api/apps/{id}/stop", s.requireAuth(s.handleAppAction("stop")))
	mux.HandleFunc("POST /api/apps/{id}/restart", s.requireAuth(s.handleAppAction("restart")))
	mux.HandleFunc("GET /api/apps/{id}/deployments", s.requireAuth(s.handleListDeployments))
	mux.HandleFunc("GET /api/apps/{id}/logs/stream", s.requireAuth(s.handleAppLogStream))
	mux.HandleFunc("GET /api/apps/{id}/terminal", s.requireAuth(sessionOnly(s.handleTerminal)))
	mux.HandleFunc("GET /api/apps/{id}/domains", s.requireAuth(s.handleListDomains))
	mux.HandleFunc("POST /api/apps/{id}/domains", s.requireAuth(s.handleCreateDomain))

	mux.HandleFunc("GET /api/deployments/{id}", s.requireAuth(s.handleGetDeployment))
	mux.HandleFunc("POST /api/deployments/{id}/cancel", s.requireAuth(s.handleCancelDeployment))
	mux.HandleFunc("GET /api/deployments/{id}/log", s.requireAuth(s.handleDeploymentLog))
	mux.HandleFunc("GET /api/deployments/{id}/log/stream", s.requireAuth(s.handleDeploymentLogStream))
	mux.HandleFunc("PATCH /api/domains/{id}", s.requireAuth(s.handleUpdateDomain))
	mux.HandleFunc("DELETE /api/domains/{id}", s.requireAuth(s.handleDeleteDomain))
	mux.HandleFunc("GET /api/domains/{id}/dns", s.requireAuth(s.handleDomainDNS))

	mux.HandleFunc("GET /api/events", s.requireAuth(s.handleListEvents))
	mux.HandleFunc("GET /api/host/metrics", s.requireAuth(s.handleHostMetrics))
	mux.HandleFunc("GET /api/docker/metrics", s.requireAuth(s.handleDockerMetrics))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	mux.Handle("/", s.spa())

	return s.recoverPanics(s.logRequests(securityHeaders(sameOrigin(mux))))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "unhealthy", "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleMeta is public: the web app reads the product name and setup state from here.
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	required, err := s.accounts.SetupRequired(r.Context())
	if err != nil {
		s.internal(w, "meta", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":           brand.Name,
		"slug":           brand.Slug,
		"version":        s.version,
		"setup_required": required,
	})
}

func (s *Server) internal(w http.ResponseWriter, what string, err error) {
	s.log.Error(what, "error", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal server error")
}

// StopStreams ends every open event stream. Call it when the HTTP server starts shutting
// down: a stream never finishes on its own, so it would hold the shutdown until it times out.
func (s *Server) StopStreams() { s.stopOnce.Do(func() { close(s.stop) }) }

// streamContext is the request context, also cancelled by StopStreams.
func (s *Server) streamContext(r *http.Request) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	go func() {
		select {
		case <-s.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
