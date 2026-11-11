// Command panel is the server and its recovery CLI in one binary.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/moby/moby/client"

	"panel/internal/account"
	"panel/internal/api"
	"panel/internal/brand"
	"panel/internal/config"
	"panel/internal/deploy"
	"panel/internal/executor"
	"panel/internal/proxy"
	"panel/internal/secret"
	"panel/internal/store"
	"panel/internal/update"
	"panel/internal/webui"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, brand.Slug+":", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `%[1]s %[3]s

Usage:
  %[2]s serve                                        start the panel
  %[2]s setup-token                                  print a new one-time setup link (15 min)
  %[2]s owner create --email E [--password P]        create an owner account
  %[2]s owner reset-password [--email E] [--password P]
                                                     set a new password and sign out all sessions
  %[2]s update [--check] [--yes]                     update the panel to the newest release
  %[2]s version

Environment:
  %[4]sDATA_DIR    data directory (default %[5]s)
  %[4]sADDR        listen address (default :8080)
  %[4]sPUBLIC_URL  base URL used when printing links
  %[4]sDATA_VOLUME Docker volume holding the data dir, when the panel runs in a container
  %[4]sPROXY_HTTP_PORT, %[4]sPROXY_HTTPS_PORT
                    host ports of the proxy (default 80, 443)
  %[4]sPROXY_ADMIN  how the panel reaches Caddy's admin API: socket (default on Linux) or tcp
`, brand.Name, brand.Slug, version, brand.EnvPrefix(), brand.DataDir())
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("missing command")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := config.Load()

	switch args[0] {
	case "serve":
		return serve(ctx, cfg)
	case "update":
		return updateCmd(ctx, cfg, args[1:])
	case "update-apply": // run by the update helper container, not by people
		return updateApply(ctx, cfg, args[1:])
	case "setup-token":
		return withStore(ctx, cfg, func(st *store.Store, acc *account.Service) error {
			return printSetupLink(ctx, cfg, acc)
		})
	case "owner":
		if len(args) < 2 {
			usage()
			return errors.New("owner: missing subcommand (create, reset-password)")
		}
		return owner(ctx, cfg, args[1], args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return nil
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// open prepares the data directory, database (migrated) and encryption key.
func open(ctx context.Context, cfg config.Config, log *slog.Logger) (*store.Store, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	if _, err := secret.LoadOrCreate(cfg.KeyPath()); err != nil {
		return nil, err
	}
	st, err := store.Open(cfg.DBPath(), cfg.BackupDir())
	if err != nil {
		return nil, err
	}
	applied, err := st.Migrate(ctx)
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if len(applied) > 0 && log != nil {
		log.Info("migrations applied", "versions", applied)
	}
	return st, nil
}

func withStore(ctx context.Context, cfg config.Config, fn func(*store.Store, *account.Service) error) error {
	st, err := open(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer st.Close()
	return fn(st, account.New(st))
}

func serve(ctx context.Context, cfg config.Config) error {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	st, err := open(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer st.Close()
	acc := account.New(st)

	if required, err := acc.SetupRequired(ctx); err != nil {
		return err
	} else if required {
		if err := printSetupLink(ctx, cfg, acc); err != nil {
			return err
		}
	}

	ex, err := executor.NewDocker(executor.Options{})
	if err != nil {
		return err
	}
	defer ex.Close()
	prepareDocker(ctx, ex, st, log)

	px, err := proxy.New(proxy.Options{
		Executor:      ex,
		Log:           log,
		Name:          brand.Slug + "-proxy",
		Image:         cfg.Proxy.Image,
		Dir:           cfg.ProxyDir(),
		Volume:        cfg.DataVolume,
		VolumePath:    "proxy", // cfg.ProxyDir() relative to the data dir
		HTTPPort:      cfg.Proxy.HTTPPort,
		HTTPSPort:     cfg.Proxy.HTTPSPort,
		Admin:         cfg.Proxy.Admin,
		AdminPort:     cfg.Proxy.AdminPort,
		PanelUpstream: panelUpstream(cfg),
		Source:        proxySource(st),
	})
	if err != nil {
		return err
	}
	go func() { // off the startup path: the first run pulls the Caddy image
		startCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err := px.Start(startCtx)
		cancel()
		if err != nil {
			log.Error("proxy not started; retrying in the background", "error", err)
		} else {
			log.Info("proxy ready", "http_port", cfg.Proxy.HTTPPort, "https_port", cfg.Proxy.HTTPSPort, "admin", cfg.Proxy.Admin)
		}
		px.Supervise(ctx, 30*time.Second)
	}()

	go func() { // drop expired sessions hourly
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if n, err := st.DeleteExpiredSessions(ctx); err == nil && n > 0 {
				log.Info("expired sessions removed", "count", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	deployer := deploy.New(ctx, deploy.Options{Store: st, Executor: ex, Router: px, DataDir: cfg.DataDir, Log: log})
	if n, err := deployer.Recover(ctx); err != nil {
		return err
	} else if n > 0 {
		log.Warn("deployments interrupted by the last shutdown were marked failed", "count", n)
	}

	apiSrv := api.New(st, acc, log, version, webui.FS())
	apiSrv.SetProxy(px)
	apiSrv.SetDeployer(deployer)
	if updater := newUpdater(ctx, cfg, st, log); updater != nil {
		updater.Detect(ctx)
		apiSrv.SetUpdater(updater)
		go updater.Run(ctx, time.Hour, func(ctx context.Context) bool {
			v, _, _ := st.GetSetting(ctx, store.SettingUpdateAuto)
			return v == "true"
		})
	}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	// Open log streams would otherwise hold a graceful shutdown until its timeout.
	srv.RegisterOnShutdown(apiSrv.StopStreams)
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info(brand.Slug+" started", "addr", cfg.Addr, "version", version, "data_dir", cfg.DataDir)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	deployer.Wait() // ctx is cancelled: running deployments fail fast and record it
	return err
}

// prepareDocker makes sure the shared network exists and takes stock of the containers the
// panel already manages. A missing daemon is logged, not fatal: the panel must still start
// so the owner can sign in and see what is wrong.
func prepareDocker(ctx context.Context, ex *executor.Docker, st *store.Store, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ex.Ping(ctx); err != nil {
		log.Warn("docker unavailable; deploys will fail until it is reachable", "error", err)
		return
	}
	if err := ex.EnsureNetwork(ctx); err != nil {
		log.Error("docker network", "network", ex.Network(), "error", err)
		return
	}
	report, err := executor.Reconcile(ctx, ex, func(kind executor.Kind, id string) bool {
		if kind == executor.KindApp {
			ok, err := st.AppExists(ctx, id)
			return ok && err == nil
		}
		return proxy.IsProxy(kind, id)
	})
	if err != nil {
		log.Error("docker reconcile", "error", err)
		return
	}
	log.Info("docker ready", "network", ex.Network(), "adopted", len(report.Adopted), "orphans", len(report.Orphans))
	for _, c := range report.Orphans {
		log.Warn("orphan container", "name", c.Name, "id", c.ID[:min(12, len(c.ID))], "kind", c.Kind, "resource", c.ResourceID, "state", c.State)
	}
}

// proxySource reads the routing state from the database: the panel's own domain and one
// route per app that has domains.
func proxySource(st *store.Store) func(context.Context) (proxy.Config, error) {
	return func(ctx context.Context) (proxy.Config, error) {
		domain, _, err := st.GetSetting(ctx, store.SettingPanelDomain)
		if err != nil {
			return proxy.Config{}, err
		}
		email, _, err := st.GetSetting(ctx, store.SettingACMEEmail)
		if err != nil {
			return proxy.Config{}, err
		}
		routes, err := st.ProxyRoutes(ctx)
		if err != nil {
			return proxy.Config{}, err
		}
		panelTLS, _, err := st.GetSetting(ctx, store.SettingPanelTLS)
		if err != nil {
			return proxy.Config{}, err
		}
		ipOff, _, err := st.GetSetting(ctx, store.SettingPanelIPOff)
		if err != nil {
			return proxy.Config{}, err
		}
		cfg := proxy.Config{
			PanelDomain:     domain,
			PanelHTTPOnly:   panelTLS == store.TLSHTTPOnly,
			PanelDomainOnly: ipOff == "true",
			ACMEEmail:       email,
		}
		for _, r := range routes {
			route := proxy.Route{ID: r.AppID, Hosts: r.Hosts, HTTPOnly: r.HTTPOnly}
			if r.Upstream != "" {
				route.Upstreams = []string{r.Upstream}
			}
			cfg.Routes = append(cfg.Routes, route)
		}
		return cfg, nil
	}
}

// panelUpstream is the address Caddy uses to reach the panel. In its container the panel
// is on the shared network under the product slug; run straight on a host (development)
// it is reached through the Docker host gateway.
func panelUpstream(cfg config.Config) string {
	if cfg.Proxy.PanelUpstream != "" {
		return cfg.Proxy.PanelUpstream
	}
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil || port == "" {
		port = "8080"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return net.JoinHostPort(brand.Slug, port)
	}
	return net.JoinHostPort("host.docker.internal", port)
}

func printSetupLink(ctx context.Context, cfg config.Config, acc *account.Service) error {
	token, expires, err := acc.IssueSetupToken(ctx)
	if err != nil {
		return err
	}
	base := strings.TrimRight(cfg.PublicURL, "/")
	if base == "" {
		base = "http://<server-ip>"
	}
	fmt.Printf("\n  Finish setting up %s:\n\n    %s/setup?token=%s\n\n  The link is valid until %s (15 minutes) and works once.\n\n",
		brand.Name, base, token, expires.Local().Format("15:04:05"))
	return nil
}

func owner(ctx context.Context, cfg config.Config, sub string, args []string) error {
	fs := flag.NewFlagSet("owner "+sub, flag.ContinueOnError)
	email := fs.String("email", "", "owner email")
	password := fs.String("password", "", "password (read from stdin when omitted)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if sub != "create" && sub != "reset-password" {
		return fmt.Errorf("owner: unknown subcommand %q (create, reset-password)", sub)
	}
	if sub == "create" && *email == "" {
		return errors.New("owner create: --email is required")
	}
	if *password == "" {
		fmt.Fprint(os.Stderr, "Password (input is visible): ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return errors.New("no password given")
		}
		*password = strings.TrimRight(line, "\r\n")
	}
	return withStore(ctx, cfg, func(st *store.Store, acc *account.Service) error {
		if sub == "create" {
			u, err := acc.CreateOwner(ctx, *email, *password)
			if err != nil {
				return err
			}
			fmt.Printf("Owner %s created.\n", u.Email)
			return nil
		}
		u, err := acc.ResetPassword(ctx, *email, *password)
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("no matching owner account")
		}
		if err != nil {
			return err
		}
		fmt.Printf("Password updated for %s; all sessions signed out.\n", u.Email)
		return nil
	})
}

// newUpdater sets up self-updates. Without a Docker client it returns nil and the UI says
// updates are unavailable.
func newUpdater(ctx context.Context, cfg config.Config, st *store.Store, log *slog.Logger) *update.Service {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		log.Warn("self-update disabled", "error", err)
		return nil
	}
	return update.New(update.Options{
		Docker:  cli,
		DataDir: cfg.DataDir,
		Version: version,
		Log:     log,
		Busy: func(ctx context.Context) (bool, error) {
			n, err := st.CountActiveDeployments(ctx)
			return n > 0, err
		},
		OnResult: func(r update.Result) { recordUpdateResult(ctx, r, st, log) },
	})
}

// recordUpdateResult turns a helper result into an activity event, once per result
// (remembered across restarts in a setting).
func recordUpdateResult(ctx context.Context, r update.Result, st *store.Store, log *slog.Logger) {
	stamp := r.FinishedAt.UTC().Format(time.RFC3339Nano)
	if seen, _, _ := st.GetSetting(ctx, store.SettingUpdateSeen); seen == stamp {
		return
	}
	action, detail := "panel.updated", r.From+" → "+r.To
	if !r.OK {
		action, detail = "panel.update_failed", r.Error
	}
	if err := st.AddEvent(ctx, store.Event{Actor: "system", Action: action, TargetType: "panel", TargetName: r.To, Detail: detail}); err != nil {
		log.Error("record update event", "error", err)
		return
	}
	_ = st.SetSetting(ctx, store.SettingUpdateSeen, stamp)
}

// updateCmd is `update` on the command line: the same check and swap as Settings → Updates,
// for a panel reached over SSH. It runs inside the panel's container (the host wrapper
// execs into it), so the panel stops a moment after the helper starts, and so does this.
func updateCmd(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	checkOnly := fs.Bool("check", false, "only report whether an update is available")
	yes := fs.Bool("yes", false, "update without asking")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withStore(ctx, cfg, func(st *store.Store, _ *account.Service) error {
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		u := newUpdater(ctx, cfg, st, log)
		if u == nil {
			return errors.New("updates need the Docker socket, which this process cannot reach")
		}
		fmt.Println("Checking for updates…")
		info := u.Check(ctx)
		fmt.Printf("Running   %s (%s)\n", info.Version, orDash(info.Image))
		switch {
		case !info.Supported:
			return errors.New("updates are not supported here: " + info.Reason)
		case info.Error != "":
			return errors.New(info.Error)
		case !info.Available:
			fmt.Println("Up to date.")
			return nil
		}
		fmt.Printf("Available %s (%s)\n", info.Latest, info.LatestImage)
		if *checkOnly {
			return nil
		}
		if !*yes {
			fmt.Print("Update now? The panel restarts; deployed apps keep running. [y/N] ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				fmt.Println("Not updated.")
				return nil
			}
		}
		fmt.Printf("Pulling %s…\n", info.LatestImage)
		if err := u.Apply(ctx); err != nil {
			return err
		}
		_ = st.AddEvent(ctx, store.Event{Actor: "cli", Action: "panel.update", TargetType: "panel",
			TargetName: info.Latest, Detail: info.Image + " → " + info.LatestImage})
		fmt.Printf("The update helper is swapping the panel to %s; this session ends when the panel stops.\n"+
			"Follow it with: docker logs -f %s-updater\nThen check with: %s version\n", info.Latest, brand.Slug, brand.Slug)
		return nil
	})
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// updateApply is the helper's side of an update: swap the panel container.
func updateApply(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("update-apply", flag.ContinueOnError)
	target := fs.String("container", "", "panel container id")
	image := fs.String("image", "", "new image")
	from := fs.String("from", "", "current image")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" || *image == "" {
		return errors.New("update-apply needs --container and --image")
	}
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return err
	}
	defer cli.Close()
	f, err := os.OpenFile(update.LogPath(cfg.DataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	// The helper must outlive the panel it stops, so it ignores the signal context's
	// cancellation from a stray stop and bounds itself instead.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()
	return update.Swap(ctx, update.SwapOptions{
		Docker: cli, DataDir: cfg.DataDir, Container: *target, Image: *image, From: *from,
		Out: io.MultiWriter(f, os.Stdout),
	})
}
