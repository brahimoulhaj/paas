# Berth

A lightweight, single-node PaaS panel: one Go binary with an embedded React app, SQLite for
state, Docker for workloads and Caddy in front. "Berth" is a working name held in
`internal/brand/brand.go`; nothing else hard-codes it.

Progress is tracked in [Roadmap.md](Roadmap.md).

## Install

On a fresh Ubuntu or Debian server (amd64 or arm64) with ports 80 and 443 open:

```sh
curl -fsSL https://github.com/brahimoulhaj/paas/releases/latest/download/install.sh | sudo sh
```

It installs Docker if needed, starts the panel and prints a one-time setup link
(`http://<server-ip>/setup?token=…`). Options go between `sudo` and `sh`, e.g.
`… | sudo BERTH_PUBLIC_URL=http://203.0.113.10 sh`. A specific version:
`https://github.com/brahimoulhaj/paas/releases/download/v0.1.1/install.sh`.

Later versions install from the panel (Settings → Updates) or over SSH with `berth update`
(`--check` only reports, `--yes` skips the prompt).

## Requirements

Go 1.26+, Node 22+, pnpm 10+, and a running Docker daemon (the panel starts without one, but
cannot deploy; the executor tests skip themselves).

## First run

```sh
make deps      # go mod tidy (creates go.sum) + pnpm install
make dev-api   # Go server on :8090, data in ./.data — prints a one-time setup link;
               # also starts Caddy in Docker on :8880 (HTTP) and :8443 (HTTPS).
               # Rebuilds and restarts on Go changes when `air` is installed (.air.toml)
make dev-web   # in a second terminal: Vite on :5173, proxies /api to :8090
```

Open the setup link from the `dev-api` output, create the owner, and you are in.

## Development with Docker (live reload)

```sh
make dev        # builds two dev images, starts them with `docker compose watch`
```

Open http://localhost:5173. Edits show up on their own:

- `web/**` is synced into the Vite container and hot-swapped in the browser (about 1 s).
- `cmd/` and `internal/` are synced into the API container, which restarts and recompiles
  (about 2 s). The browser keeps working after the restart.
- `go.mod`, `go.sum`, `web/package.json` and the lockfile rebuild the images.

Stop it with Ctrl-C, or `make dev-down` from another terminal. Caddy (`berth-proxy`) and
deployed apps keep running, as they would during a panel update; the next start adopts them.

Panel data (database, key, certificates, deployment logs) lives in the Docker volume
`berth-data`, not in `./.data`. The panel is told so (`BERTH_DATA_VOLUME`) and mounts Caddy's
folders from that volume. It survives `make dev-down`; `docker compose down -v` deletes it.
`make dev-import-data` replaces its content with a copy of `./.data` (from `make dev-api`).

Don't run `make dev` and `make dev-api` at the same time: two panels on one Docker host
fight over the proxy container.

## Build

```sh
make build              # bin/berth, web app embedded
make test               # go vet, go test, web typecheck
make image VERSION=0.1.0
```

## Releases

Push a tag `vX.Y.Z`: the Release workflow publishes `ghcr.io/brahimoulhaj/paas:X.Y.Z` (and
`:latest`) for amd64 and arm64 and attaches the matching `install.sh` to a GitHub release.
Installed panels see the new tag in Settings → Updates.

## Install on a server

```sh
make image VERSION=0.1.0                       # local build, tagged ghcr.io/brahimoulhaj/paas:0.1.0
make installer VERSION=0.1.0 [IMAGE=...]       # writes bin/install.sh from install/install.sh.tmpl
```

On the server (Linux, amd64 or arm64), as root: `curl -fsSL <url>/install.sh | sh`. It checks
ports 80/443, installs Docker from get.docker.com when missing, creates `/var/lib/berth` and the
`berth` network, writes `/var/lib/berth/compose.yaml` and starts the panel, then prints the
setup link. It also installs a `berth` host command that runs the CLI inside the container
(`berth setup-token`, `berth owner reset-password`). Running it again keeps the data.

Overrides: `BERTH_IMAGE`, `BERTH_PUBLIC_URL`, and `BERTH_PULL=never` to use an image already
loaded with `docker load` (no registry needed for testing).

## CLI

```
berth serve
berth setup-token                                  new one-time setup link (15 min)
berth owner create --email E [--password P]
berth owner reset-password [--email E] [--password P]
berth version
```

Configuration is by environment: `BERTH_DATA_DIR` (default `/var/lib/berth`), `BERTH_ADDR`
(default `:8080`), `BERTH_PUBLIC_URL` (base URL for printed links), `BERTH_MAX_BUILDS` (Git builds
run at once, default 1; further Git deploys wait, queued, and can be cancelled).

The dashboard samples host CPU, RAM, swap, load, uptime, data-disk capacity and network
traffic every five seconds, keeping ten minutes of history in memory. The disk is the
filesystem holding `BERTH_DATA_DIR`; available space excludes filesystem reservations.
For host network readings inside Docker, mount `/proc:/host/proc:ro` on the panel
container (included in the installer and development Compose file). Existing installations
can re-run the installer or add that mount and recreate the panel container. Image-only
updates preserve existing mounts. `BERTH_HOST_PROC` overrides the proc path for custom
setups. On Docker Desktop these are the Linux VM's metrics. Unavailable readings are
shown explicitly, and loopback, container bridges and tunnels are excluded from network
totals. Metrics are available only through the authenticated `/api/host/metrics` endpoint.

The Docker dashboard card uses the connected daemon's read-only API to show container
states, images, volumes, networks, engine configuration and per-resource storage usage.
Counts refresh every 15 seconds; aggregate disk scans run at most once a minute.
Sizes and reclaimable space are Docker's estimates, exclude bind mounts and container
logs, and are shown by category because image and build-cache layers may overlap.
If Docker becomes unavailable, the card labels the last known readings. The endpoint
is `/api/docker/metrics` and requires authentication.

Proxy: `BERTH_PROXY_HTTP_PORT` / `BERTH_PROXY_HTTPS_PORT` (default 80 / 443; `make dev-api` uses
8880 / 8443), `BERTH_PROXY_IMAGE`, `BERTH_PROXY_PANEL_UPSTREAM` (how Caddy reaches the panel),
`BERTH_PROXY_ADMIN` (`socket` on Linux, `tcp` elsewhere) and `BERTH_PROXY_ADMIN_PORT` (tcp mode,
default 2019 on 127.0.0.1). Socket mode needs the panel to run as root and the data dir mounted
at the same path inside the panel container as on the host.

One panel per Docker host: the network, the proxy container and the container labels are all
named after the product slug, so two panels on the same daemon would fight over them.

## Layout

```
cmd/panel/            main: serve, owner, setup-token
internal/brand/       product name and slug — the only place they live
internal/config/      environment configuration
internal/store/       SQLite queries and embedded migrations
internal/secret/      AES-GCM encryption with a key file in the data dir
internal/auth/        password hashing, opaque tokens
internal/account/     owner setup, login, recovery (shared by API and CLI)
internal/executor/    container host seam; local Docker implementation
internal/proxy/       Caddy container, admin API client, config builder
internal/deploy/      deploy pipeline: pull, start, switch route, remove old; start/stop/restart
internal/api/         HTTP handlers, middleware, embedded SPA
internal/webui/       go:embed target for the built web app
web/                  React + TypeScript + Vite + Tailwind
```
