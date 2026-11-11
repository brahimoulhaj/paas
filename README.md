# Berth

A lightweight, single-node PaaS panel: one Go binary with an embedded React app, SQLite for
state, Docker for workloads and Caddy in front. "Berth" is a working name held in
`internal/brand/brand.go`; nothing else hard-codes it.

Progress is tracked in [Roadmap.md](Roadmap.md).

## Requirements

Go 1.26+, Node 22+, pnpm 10+, and a running Docker daemon (the panel starts without one, but
cannot deploy; the executor tests skip themselves).

## First run

```sh
make deps      # go mod tidy (creates go.sum) + pnpm install
make dev-api   # Go server on :8080, data in ./.data — prints a one-time setup link;
               # also starts Caddy in Docker on :8880 (HTTP) and :8443 (HTTPS).
               # Rebuilds and restarts on Go changes when `air` is installed (.air.toml)
make dev-web   # in a second terminal: Vite on :5173, proxies /api to :8080
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

## Install on a server

```sh
make image VERSION=0.1.0                       # or IMAGE=ghcr.io/you/berth to tag for a registry
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
(default `:8080`), `BERTH_PUBLIC_URL` (base URL for printed links).

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
