// Package config reads runtime configuration from the environment.
package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"panel/internal/brand"
)

type Config struct {
	DataDir   string // SQLite file, encryption key, backups, Caddy data
	Addr      string // HTTP listen address
	PublicURL string // optional, used when printing setup links
	// DataVolume names the Docker volume that holds DataDir, when the panel runs in a
	// container with its data in a named volume. Containers the panel starts then mount
	// from that volume; otherwise they bind-mount host paths under DataDir.
	DataVolume string
	// MaxBuilds is how many Git builds run at once; more deploys wait, queued.
	MaxBuilds int
	Proxy     Proxy
}

// Proxy configures the Caddy container in front of everything.
type Proxy struct {
	Image         string // Caddy image
	HTTPPort      int    // host port for HTTP
	HTTPSPort     int    // host port for HTTPS
	Admin         string // how the panel reaches Caddy's admin API: "socket" or "tcp"
	AdminPort     int    // tcp mode only: admin port published on 127.0.0.1
	PanelUpstream string // how Caddy reaches the panel, host:port
}

// Load reads <PREFIX>DATA_DIR, <PREFIX>DATA_VOLUME, <PREFIX>MAX_BUILDS, <PREFIX>ADDR, <PREFIX>PUBLIC_URL and the <PREFIX>PROXY_* variables.
func Load() Config {
	return Config{
		DataDir:    env("DATA_DIR", brand.DataDir()),
		Addr:       env("ADDR", ":8080"),
		PublicURL:  env("PUBLIC_URL", ""),
		DataVolume: env("DATA_VOLUME", ""),
		MaxBuilds:  envInt("MAX_BUILDS", 1),
		Proxy: Proxy{
			Image:         env("PROXY_IMAGE", "caddy:2.10.2-alpine"),
			HTTPPort:      envInt("PROXY_HTTP_PORT", 80),
			HTTPSPort:     envInt("PROXY_HTTPS_PORT", 443),
			Admin:         env("PROXY_ADMIN", defaultProxyAdmin()),
			AdminPort:     envInt("PROXY_ADMIN_PORT", 2019),
			PanelUpstream: env("PROXY_PANEL_UPSTREAM", ""),
		},
	}
}

// defaultProxyAdmin is a unix socket in the data dir on Linux: no other container on the
// shared network can reach it. Docker on macOS and Windows runs in a VM where a socket in
// a bind mount cannot be dialled from the host, so development there uses a TCP port
// published on 127.0.0.1 instead.
func defaultProxyAdmin() string {
	if runtime.GOOS == "linux" {
		return "socket"
	}
	return "tcp"
}

func (c Config) ProxyDir() string { return filepath.Join(c.DataDir, "proxy") }

func (c Config) DBPath() string    { return filepath.Join(c.DataDir, brand.Slug+".db") }
func (c Config) KeyPath() string   { return filepath.Join(c.DataDir, "secret.key") }
func (c Config) BackupDir() string { return filepath.Join(c.DataDir, "backups") }

func envInt(name string, fallback int) int {
	if n, err := strconv.Atoi(env(name, "")); err == nil && n > 0 {
		return n
	}
	return fallback
}

func env(name, fallback string) string {
	if v := os.Getenv(brand.EnvPrefix() + name); v != "" {
		return v
	}
	return fallback
}
