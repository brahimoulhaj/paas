# Berth — Roadmap

Implementation tracker for the single-node PaaS panel. Source of truth for the design is the
architecture doc ("Single-node PaaS panel: architecture and data model"). "Berth" is the working
name; it lives only in `internal/brand/brand.go`.

**How to use:** tick a box only when the item is built *and* verified (test or manual run).
`[ ]` not started · `[~]` in progress · `[x]` done.

**Progress:** M0 ◐ · M1 ◐ · M2 ◐ (nearly done) · M3 ◐ · M4 ☐ · M5 ☐ · M6 ◐

**Where we are (2026-11-13, v0.6.0):** M2 is done except pushing builds to a registry, more builders (Nixpacks, Buildpacks, Static), a Gitea token, a build-time env flag and cron. M3 has push/CI webhooks, health checks before the traffic switch, rollback, and the deploy state machine with its timeline; the rolling strategy with a real drain is next. v0.6.0 checked on the VPS (2026-11-14): build caps, resume after restart, deploy keys, private registries, deploy webhooks, Railpack without a Dockerfile, cancel in the browser.

**Next up:**
1. On the VPS (manual), once released: save an HTTP check on an app and deploy (log shows "Health check passed"); set a wrong path or status and deploy (fails after the start period, the old container keeps serving); create a new app and check it gets the TCP check. Roll a Git app back to an earlier commit (no build in the log).
2. Code, M3: rolling strategy with a real drain (default 10 s before the old container goes), then recreate for apps with volumes or host ports (adds the `rolled_back` exit).
3. Then: push builds to a registry (closes M2), or M4 databases (Postgres, MariaDB, S3 backups).
4. Check whether "Build log stream on the deployment page" is already covered by the deployment log (Git build output streams there with `|` lines) and tick it if so.
5. Record the fresh-install time with image download (M1, "Under 60 seconds").
---

## M0 — Project skeleton

Everything needed before the first feature: repo layout, tooling, and the shared plumbing.

### Repository and tooling
- [x] Go module (`panel`, brand-neutral so a rename never touches imports), `cmd/panel` entrypoint, package layout from the doc
- [x] `internal/brand` — `Name` and `Slug` constants; every path, network, env prefix derives from them
- [x] `internal/config` — data dir, listen address, env vars with the `BERTH_` prefix
- [x] Makefile: `deps`, `dev-api`, `dev-web`, `web`, `build`, `test`, `image`
- [ ] Linting (golangci-lint, eslint)
- [x] Multi-stage `Dockerfile` (web build → Go build → slim runtime image) — `make image`, 36 MB, runs on a fresh Ubuntu 24.04 arm64 VM
- [x] `.gitignore`, `.editorconfig`, README with dev setup
- [x] CI: vet, test (`-short -race`), web typecheck and build, installer renders — `.github/workflows/ci.yml`, green on `main`
- [ ] Decide license (open question in the doc)

### Store (`internal/store`)
- [x] SQLite via `modernc.org/sqlite` (pure Go), WAL mode, foreign keys on, busy timeout — `make test` green with the real driver (2026-11-10)
- [x] Migrations embedded in the binary, run at startup (small built-in runner, `internal/store/migrate.go`; goose can replace it if down-migrations are ever needed)
- [~] Pre-migration backup with `VACUUM INTO` — runs when migration 0002 is applied to an existing database; not yet checked by restoring one
- [x] ULID ids (strictly increasing within a process), `created_at` / `updated_at` on every table
- [ ] sqlc for typed queries
- [x] `internal/secret` — AES-GCM encryption with a key file in the data dir

### HTTP API (`internal/api`)
- [x] Router (std `net/http`), request logging, panic recovery, security headers, JSON error format
- [x] `GET /api/health`
- [x] `GET /api/meta` — product name, slug, version, setup state (the UI hard-codes no product string)
- [x] Serve the embedded SPA with client-side-routing fallback
- [x] SSE helper for log and status streams (`internal/api/sse.go`); open streams end on shutdown

### Web app (`web/`)
- [x] Vite + React + TypeScript + Tailwind
- [x] TanStack Router and TanStack Query
- [~] ReUI / shadcn components copied into the repo — searchable combobox (`web/src/components/combobox.tsx`, shadcn's combobox on Base UI, the one ReUI builds on, restyled with the theme tokens); used for repository access, repository and branch; other components as they are needed
- [x] Theme tokens: zero radius, 1 px borders, monospace body, pixel font for headings and badges, one accent on a dark base
- [x] App shell: sidebar, sign-out, overview page with projects, empty state and recent activity
- [x] API client with typed errors; product name read from `/api/meta`
- [x] Dev proxy to the Go server; production build embedded with `go:embed`
- [~] Languages: English, French, Arabic (right to left) and Spanish — typed dictionaries per screen in `web/src/i18n`, `web/src/lib/i18n.tsx` (choice kept in the browser, defaults to the browser's language), searchable language menu on the globe button of the navbar and on the sign-in screens; messages written by the server (API errors, deployment logs) stay in English

---

## M1 — Foundation

Goal: install on a real VPS, create the owner, deploy a public image, attach a domain with automatic TLS, watch live logs.

### Auth and owner
- [x] Migration: `users`, `sessions`, `api_tokens`, `settings`
- [x] Password hashing — PBKDF2-HMAC-SHA256, 600k iterations (stdlib); the stored format carries an algorithm id so argon2id can be added later
- [x] One-time setup token, valid 15 minutes, stored hashed
- [x] `POST /api/setup` — create the owner with a valid setup token
- [x] `POST /api/auth/login`, `POST /api/auth/logout`, `GET /api/auth/me`
- [x] Cookie sessions (HttpOnly, SameSite, Secure when on HTTPS), expiry and cleanup
- [x] Auth middleware; CSRF protection for cookie-authenticated writes (same-origin check)
- [x] Login and setup rate limiting (per IP, in memory; the client IP comes from Caddy's `X-Forwarded-For` when the peer is private)
- [x] API tokens: create, list, revoke, bearer auth (`/api/tokens`, `Authorization: Bearer berth_…`); token management needs the browser session; last use recorded at most once a minute; events name the token. Checked by `internal/api/tokens_test.go` and in a browser against the dev stack

### CLI (same binary)
- [x] `berth serve`
- [x] `berth owner create`
- [x] `berth owner reset-password`
- [x] `berth setup-token`
- [x] `berth version`

### Executor (`internal/executor`)
Verified by `internal/executor/docker_test.go` against a real daemon (skipped with `-short` or when no daemon answers).

- [x] `Executor` interface — the seam for multi-server later
- [x] Local Docker implementation over the mounted socket (`github.com/moby/moby/client`, API version negotiated)
- [x] Ensure the `berth` Docker network exists (at `serve` startup)
- [x] Image pull with progress
- [x] Container create / start / stop / remove / inspect, with labels identifying panel-managed containers (`berth.managed`, `berth.kind`, `berth.resource`); json-file log rotation 10 MB × 3
- [x] Container log streaming (follow, tail, since), stdout and stderr demultiplexed
- [x] CPU and memory limits
- [x] Reconcile on startup: adopt running managed containers (proxy and apps), log orphans; unfinished deployments are resumed (see Jobs and queue)

### Proxy (`internal/proxy`)
Verified by `internal/proxy/manager_test.go` against a real Caddy container (skipped with `-short` or when no daemon answers).

- [x] Start and supervise the Caddy container (ports 80/443, data under `<data dir>/proxy`); recreated when its spec changes, checked every 30 s
- [x] Caddy admin API client — unix socket in the data dir on Linux, TCP on 127.0.0.1 for development on macOS/Windows
- [x] Config builder: full Caddy JSON derived from SQLite (panel domain and one route per app with domains)
- [x] Rebuild and push the full config on startup (also written to the file Caddy boots from)
- [x] Route `http://<server-ip>` to the panel UI
- [x] Panel domain with automatic TLS — `GET/PUT /api/settings/panel`, verified with Caddy's internal CA; on a real VPS as `http_only` behind Cloudflare's proxy (`ops.azayr.com`) and with a Let's Encrypt certificate on a direct (unproxied) domain (2026-11-11); settings screen and onboarding step done
- [x] Per-app route with automatic TLS (`tls_mode = auto`) — verified end to end with Caddy's internal CA on a `.test` name, and on a real VPS with a public domain and a Let's Encrypt certificate (2026-11-11)
- [x] Atomic upstream patch for a single route
- [x] Socket admin mode end to end on a Linux host — installed panel on an Ubuntu 24.04 VM: `proxy ready admin=socket`, deploy and route switch work (2026-11-11)

### Projects and apps (image source only)
Verified by unit tests (fake container host) and one manual end-to-end run against real Docker and Caddy: deploy, redeploy with zero failed requests, failed deploy keeps the old container, stop/start, panel restart, delete.

- [x] Migration: `projects`, `apps`, `domains`, `deployments`, `events` (`0002_projects_apps.sql`)
- [x] Projects CRUD — `/api/projects`
- [x] Apps CRUD — source type `image`, public registry, port, CPU and memory limits — `/api/projects/{id}/apps`, `/api/apps/{id}`
- [x] Domains: add, remove, hostname validation, DNS check hint — `/api/apps/{id}/domains`, `/api/domains/{id}/dns`
- [x] Primary domain per app (`0007_primary_domain.sql`): the first domain added is primary, can be switched (`PATCH /api/domains/{id}`), and is handed to the oldest remaining domain when the primary is removed; one primary per app enforced by a unique index
- [x] Minimal deploy: pull → start → switch route → remove old (no health checks yet; a container that exits or restarts within 3 s fails the deploy) — `internal/deploy`
- [x] Deployment record with status and log file path — `/api/deployments/{id}`, `/api/deployments/{id}/log`
- [x] Start / stop / restart an app
- [x] `events` rows for every mutating action (audit log, activity feed) — `/api/events`
- [ ] Automated integration test of a deploy against a real Docker daemon (today's end-to-end run was a throwaway script)

### Live logs
Verified by API tests (with the race detector) and in a browser against a real nginx container: lines appear as requests arrive, search, pause, wrap, follow, stop and start.

- [x] SSE endpoint for container runtime logs — `/api/apps/{id}/logs/stream`; resumes after the last line on reconnect
- [x] SSE endpoint for deployment logs, persisted per deployment — `/api/deployments/{id}/log/stream`; follows the file, resumes by byte offset
- [x] Log viewer in the UI (follow, pause, search, wrap); keeps the last 5000 lines

### Installer
- [x] `install.sh` generated from the brand constants — `make installer` fills `install/install.sh.tmpl`; shellcheck clean (hooking it into a release pipeline is under Cross-cutting)
- [x] Checks: root, OS, architecture, ports 80 and 443 free (skipped on a re-run, which finds its own proxy there)
- [x] Docker Engine check and install from Docker's official script — verified on a VM without Docker (installed 29.9.3 with Compose)
- [x] Create `/var/lib/berth` and the Docker network
- [x] Pull and start the panel image with the socket and data dir mounted — Compose project in `/var/lib/berth/compose.yaml`; installed on a real VPS from `ghcr.io/brahimoulhaj/paas` with the one-command install (2026-11-11)
- [x] Print the setup URL with the one-time token; `berth` host command wraps the CLI in the container
- [~] Under 60 seconds on a server that already has Docker — 2 s with the images already local; image download time from a registry still to measure

### Updates
- [x] Short-lived updater container that pulls the new image and recreates the panel — `internal/update` (registry check by semver tag or digest, helper runs `update-apply` to swap the container, result recorded as an event); registry and compose-rewrite logic unit-tested; on a real VPS the panel updated itself 0.1.x → 0.2.0 → 0.2.1 from GHCR (2026-11-11)
- [x] `berth update` and an update button with the available version — Settings → Updates: check, update now, hourly automatic updates (wait while a deploy runs); `berth update [--check] [--yes]` (same check and helper; refuses while a helper is already swapping); on the VPS the CLI swapped the panel to 0.2.4 (2026-11-11)

### UI
Checked in a browser against a real deploy (create project and app, add domain, deploy, failed redeploy, stop/start, delete), desktop and phone width.

- [x] Setup screen (token → owner email and password)
- [x] Login screen
- [~] Onboarding stepper — `/onboarding`, shown after setup and until finished or skipped (saved on the server: `/api/onboarding`), reachable again from Settings. Steps today: panel domain, first deploy. S3, registry and notifications steps arrive with M4, M5 and M6
- [x] Projects list and project page (create, rename, delete)
- [x] Create-app flow (image)
- [x] App page: overview, deployments (live log), logs (live container output), domains (with DNS hint), settings
- [x] Activity feed
- [x] API tokens section on the Settings page: create (secret shown once, copy, curl example), list with last use, revoke
- [x] Panel domain options: HTTP only (CDN terminates TLS) and "disable access by IP" (unknown hosts dropped; needs a domain; recovery through `127.0.0.1:8080` over SSH); both on in production on a real VPS (2026-11-11)
- [x] Settings page with the panel domain section: set, change, remove, certificate email, DNS hint (`/api/settings/panel/dns`); checked in a browser with `panel.test` through Caddy's internal CA

---

## M2 — Git and builds

Goal: push-to-deploy from a repository with any of the builders.

### Git sources
- [~] Migration: `git_sources`, `registries`, `env_vars` — `0003_git_source.sql` adds the Git columns to `apps` and `commit_sha` to `deployments`; `0004_git_sources.sql` adds `git_sources` (secrets encrypted) and `apps.git_source_id`; `0005_app_environment.sql` keeps env vars as a JSON column on `apps`; `registries` comes with its feature
- [x] Public Git URL — app `source_type = git` with an https repository and a branch (`/api/projects/{id}/apps`, `PATCH /api/apps/{id}`; URL, branch and path validation tested in `internal/api/apps_test.go`); create-app flow and settings offer Image or Git repository; deployed end to end on a real VPS (2026-11-13)
- [x] Private repo by deploy key (generate key pair, show public key) — Settings → Git connections → Add deploy key (`POST /api/git-sources/deploy-key`): an ed25519 key pair, private half encrypted with the panel key, public half shown with Copy and where to paste it on GitHub, GitLab and Gitea; apps using it give an SSH URL (`git@host:org/app.git` or `ssh://git@host:port/…`), validated against option injection (`TestSSHRepo`). The clone runs over SSH only, with the key in a private temp folder, no user config or agent, and the host key trusted on first use and pinned in `<data dir>/ssh/known_hosts`; a changed host key or a key the server refuses gives a clear error. `openssh-client` added to the images. `TestFetchOverSSH` clones from a throwaway sshd in Docker (wrong key and changed host key refused); `TestDeployKey`, API test; a private GitHub repository deployed by deploy key on a real VPS with v0.6.0 (2026-11-14)
- [x] GitHub App (first provider) — created from Settings with GitHub's manifest flow (`POST /api/git-sources/github`, callback `/api/git-sources/github/callback`), read-only on code and metadata; app JWT and cached installation tokens; `internal/gitsource` tested against a fake GitHub; created, installed and used to deploy a private repository on a real VPS (2026-11-13). Webhooks come with M3
- [~] GitLab token — gitlab.com or self-hosted over https, token checked when added (`POST /api/git-sources/gitlab`); tested against a fake GitLab; not yet tried against GitLab itself
- [ ] Gitea token
- [x] Repo and branch picker in the UI — `GET /api/git-sources/{id}/repos` and `/branches`; the app form lists the connection's repositories and the chosen one's branches; used with a real GitHub connection (2026-11-13)
- [x] Clone / fetch at a commit into a build workspace, with cleanup — shallow clone of the branch tip into `<data dir>/builds/<deployment>` (no credential prompt or host credential helper, https only), removed after the build and at startup; commit recorded on the deployment; a private repository is fetched with the connection's token, passed to git through the environment and scoped to the repository's host

### Builders (`internal/builder`)
- [x] Builder interface: source dir in, tagged local image out — `internal/builder` (`Fetch`, `Build`); the deploy pipeline builds instead of pulling for Git apps (`internal/deploy`, fake builder tests)
- [x] Auto-detect: Dockerfile if present, otherwise Railpack — builder `auto`, the default for new Git apps; checks the configured Dockerfile path inside the build context and says in the deploy log which builder it picked (`TestBuildAutoPicksBuilder`)
- [x] Dockerfile — `docker buildx build --load` with build context and Dockerfile path checked to stay inside the repository (symlinks followed); image labelled `<slug>.build=<app id>`; checked against a real daemon in `internal/builder/builder_test.go` and on a real VPS (2026-11-13)
- [x] Railpack — `railpack prepare` writes a plan beside the workspace, `docker buildx build` runs it through the pinned `railpack-frontend` on the same daemon (no separate BuildKit); the frontend drops `--label`, so a one-line `FROM` build re-tags with the build label that pruning relies on; Git apps get `PORT` set to the app port unless their env sets it. Real build in `TestBuildRailpack` (needs `railpack` on PATH or `BERTH_TEST_RAILPACK`); an app without a Dockerfile built by Railpack on a real VPS with v0.6.0 (2026-11-14)
- [x] Start command override — per app (image or Git), replaces the image's entrypoint and command with `/bin/sh -c <command>` so `$PORT` and `$(…)` expand; empty keeps the image's own; shown on the overview, set in Settings (`0008_app_start_command.sql`, `TestStartCommand`, `TestDockerEntrypointOverride`). Needed for single-module Gradle apps: Railpack 0.43.0 starts them with `*/build/libs/*jar`, which only matches subprojects (seen on a Spring Boot app from GitLab, 2026-11-13)
- [ ] Nixpacks — `nixpacks build`
- [ ] Cloud Native Buildpacks — `pack build --builder <image>` (Heroku / Paketo)
- [ ] Static — build command, output served by a small Caddy image
- [~] Builder CLIs shipped in the panel image, pinned versions — `git`, `docker-cli` and `docker-cli-buildx` from Alpine in the runtime and dev images (not pinned); Railpack 0.43.0 pinned and checksummed in both (+22.5 MB), kept equal to `builder.RailpackVersion` by `TestRailpackPinMatchesImages`
- [x] Image tag `app-<id>:<commit-sha>`; keep the last 5, prune older — after each successful deploy the newest 5 tagged build images stay (the live one always), older and dangling ones (a commit rebuilt under the same tag) are removed; Docker refuses any image a container still uses; deleting an app removes all its build images. `internal/deploy/deploy_test.go` and `internal/executor/docker_test.go` (real daemon)
- [ ] Push to the configured registry after a build

### Jobs and queue (`internal/jobs`)
- [x] SQLite-backed job queue with retries and resume after restart — the `deployments` table is the queue (a generic jobs table can come with backups and cron). On startup, queued deploys start again; a running one that had not switched traffic starts over, its half-made container removed; one that had switched is finished (old containers removed, succeeded); at most 3 starts per deploy (`attempts`, `0009_deployment_attempts.sql`). A shutdown puts running and waiting deploys back in the queue instead of failing them. Resumed deploys wait for the proxy. Clone, pull and Git credentials are retried twice (2 s, 5 s) on network errors; builds are not. `TestRecoverResumesDeployments`, `TestShutdownRequeues`, `TestRetryTemporaryFailures`, `TestTemporary`; checked against real Docker and Caddy: panel stopped mid-clone, deploy resumed at attempt 2 and succeeded (2026-11-13); panel restarted mid-deploy, deploy resumed and succeeded on a real VPS with v0.6.0 (2026-11-14)
- [x] One build at a time by default, configurable — `BERTH_MAX_BUILDS`; a Git deploy waits, still `queued`, for a build slot and says so in its log; the slot is released once the image exists (pulls take none). Queued deploys survive a restart (below) (`TestBuildsRunOneAtATime`, `TestMaxBuildsRunsInParallel`)
- [x] CPU and memory caps on builds — Settings → Builds (`GET/PUT/DELETE /api/settings/builds`, `TestBuildSettings`; checked in a browser: save, invalid memory, reset), defaults from `BERTH_BUILD_CPUS`, `BERTH_BUILD_MEMORY_MB` (unset = no cap); read at the start of every build, shared by all builds; Docker's built-in BuildKit ignores per-build limits, so capped builds run on a buildx `docker-container` builder (`moby/buildkit:v0.34.2`) created with the limits on first use and replaced when they change (an old one is removed once no build runs on it); Railpack's label step stays on the daemon's builder, which holds the loaded image. `TestBuildLimits` (stub), `TestBuildMemoryCapEnforced` (real daemon: a 400 MB step killed under a 256 MB cap); a Git app deployed under a build cap on a real VPS with v0.6.0 (2026-11-14)
- [x] Cancel a queued or running build — `POST /api/deployments/{id}/cancel` and a Cancel button on the deployment log; works until traffic switches (then 409 `not_cancellable`); the build or pull is killed, a new container removed, the live version keeps serving; status `cancelled`, event `deployment.cancelled` (`TestCancelDeployment`); build cancelled from the browser on a real VPS with v0.6.0 (2026-11-14)
- [ ] Cron scheduler (`robfig/cron`)

### Env vars and registries
- [~] Env vars CRUD, encrypted at rest, build-time flag — per-app variables passed to the container on deploy (`0005_app_environment.sql`, `internal/store/environment_test.go`, `internal/deploy/deploy_test.go`); encrypted at rest with the panel key (AES-GCM, `internal/store/environment.go`); environments stored in clear by earlier versions are encrypted at startup (`TestEnvironmentEncryptedAtRest`); no build-time flag yet (no build arguments exist)
- [x] Bulk edit / `.env` paste — `web/src/lib/environment.ts` parses and writes `.env` text (comments, `export`, quoted values)
- [x] External private registries: add, test credentials, pull with auth — the Registry page in the sidebar (`/registry`; `GET/POST/DELETE /api/registries`); adding signs in through the Docker daemon (`RegistryLogin`), a refused password is reported; the password is encrypted with the panel key and never returned; Docker Hub names are normalized to `docker.io`. A registry in use cannot be removed. `0010_registries.sql`, `TestRegistries`, `TestRegistriesAPI`, `TestPrivateRegistry` (real `registry:2` with password auth: wrong password refused, pull without credentials refused, with them works); checked in a browser against a local registry: refused password, then add, deploy and remove (2026-11-13)
- [x] App source: image from a private registry — image apps choose Registry access (public or a registry); credentials are only sent when the image is on that registry's host, checked on save and again at deploy (`TestDeployFromPrivateRegistry`); the deploy log names the account, never the password; a private image deployed from GHCR or Docker Hub on a real VPS with v0.6.0 (2026-11-14)

### UI
- [~] Git sources settings — Settings → Git connections: connect GitHub, connect GitLab, remove (refused while an app uses it); GitHub and GitLab connected for real (2026-11-13)
- [x] Create-app flow (Git) with builder choice — Automatic, Dockerfile or Railpack; the Dockerfile field hides for Railpack; the app overview shows the builder
- [ ] Build log stream on the deployment page — probably done already (Git build output streams into the deployment log); check and tick
- [x] App settings in sections — General, Source, Resources, Environment, Volumes, Webhook, Danger zone, in a side menu (a strip on phones), the section in the URL; each saves only its fields
- [x] Env vars editor — `web/src/components/environment.tsx`, with a configurable log line limit in the log viewer
- [x] Registries page — its own page (sidebar → Registry), not in Settings: add (server, user, password or read-only token), list, remove

---

## M3 — Deploy quality

Goal: zero-downtime deploys you can trust, with rollback and every trigger.

### State machine (`internal/deploy`)
- [x] States: `queued → building → starting → health_checking → switching → draining → succeeded` — `status` stays queued / running / succeeded / failed / cancelled; while running, `phase` is the step (`pulling` for an image app instead of `building`; a Git rollback goes straight to `starting`) and `phases` records each step entered with its time, closed by the final status (`0014_deployment_phases.sql`, `EnterDeploymentPhase`). A failed deployment keeps the phase it failed in. `TestDeployPhases`, `TestDeployPhasesGit`, `TestDeploymentPhasesAPI`; checked in the browser with an image app (2026-11-14). Draining is still an immediate remove (graceful drain comes with the rolling strategy)
- [~] Exits: `failed`, `cancelled`, `rolled_back` — failed and cancelled; `rolled_back` comes with the recreate strategy, the only one that rolls back by itself
- [x] Resume or fail cleanly after a panel restart mid-deploy; no orphan containers — see Jobs and queue (M2); per-state resume comes with the state machine

### Health checks
- [~] Migration: `app_health_checks`, `volumes`, `certificates` — `0012_app_health_checks.sql` (one row per app, removed with it); volumes live in `apps.mounts` (`0006`); `certificates` comes with custom TLS
- [x] `tcp` (default) — what new apps get (port accepts a connection, start period 30 s, every 2 s, 3 failures in a row). Apps created before keep the old 3-second stay-up wait until a check is saved (`LegacyHealthCheck`); the settings say so
- [x] `http` — path, expected status, interval, timeout, retries, start period — GET on the container, redirects not followed, expected status 0 = any 2xx or 3xx; failures during the start period do not count, one success passes; a crash fails at once whatever the check (`TestHealthCheck`, `TestHealthCheckStartPeriod`, `TestHealthCheckAPI`)
- [x] `running` — stays up for N seconds — for workers that do not listen
- [~] Checks run by the panel from inside the Docker network — by container name and port, before the traffic switch; a failed check removes the new container and leaves the route. A panel run on the host (`make dev-api`) cannot reach containers, so it only waits 3 s and says so in the log (`TestHealthCheckOutsideNetwork`, checked in the browser (2026-11-14)). Not yet run on the compose stack or the VPS
- [ ] Continuous health monitoring after deploy (feeds alerts in M6)

### Strategies
- [ ] Rolling: start new → health check → patch Caddy upstream → drain (default 10 s) → remove old
- [ ] Rolling failure path: remove new, leave route untouched, mark failed
- [ ] Recreate: stop old → start new → health check → roll back to previous image on failure
- [ ] Recreate forced for apps with volumes or published host ports; UI states the short gap
- [x] Volumes CRUD and mounts — named volumes, bind mounts and inline files per app (`0006_app_mounts.sql`, `internal/deploy/mounts.go`); file content written per deployment so editing never changes the live container; tested in `internal/deploy/mounts_test.go` and `internal/store/mounts_test.go`

### Rollback and triggers
- [x] Rollback to a kept image through the same pipeline, without a build — `POST /api/deployments/{id}/rollback` makes a new deployment (trigger `rollback`, `rollback_of`, `0013_deployment_rollback.sql`) with the earlier image, commit and port; start, health check and switch as usual. A Git app returns to the images still kept (5); an image app pulls the image again and its image setting follows, so a redeploy stays on it. Offered for succeeded deployments whose image is not the live one (`TestRollbackGit`, `TestRollbackImage`, `TestRollbackAPI`); checked in the browser with an image app (2026-11-14). An image app on a moving tag (`latest`) gets whatever the tag points to now
- [ ] Manual deploy (button and API)
- [~] Git push webhooks, filtered by branch and optional path — one URL per app takes push events from GitHub (JSON or its default form encoding), GitLab, Gitea and Forgejo: a push to the app's branch deploys; other branches, tags, deleted branches and other events are ignored with the reason; ping answers OK. Pushes during a deploy queue one follow-up deploy, so a burst ships the last commit (`DeployOrQueue`). `TestDeployHook`; checked against real Docker and Caddy (ping, other branch ignored, three pushes → two deploys, 2026-11-13). No path filter yet; the GitHub App's own webhook is not used (a repository webhook is). A push to a GitHub repository with the webhook deployed on a real VPS with v0.6.0 (2026-11-14)
- [x] Deploy webhook `POST /api/hooks/deploy/<token>` with optional image tag or commit — CI calls it with no event; an image app can pass `{"tag": "…"}` (or `?tag=`), which swaps only the tag of its image (`TestWithTag`). The token (24 random bytes) is the only credential: stored hashed for lookup and encrypted for display (`0011_deploy_hooks.sql`); 60 calls a minute per IP; webhook deploys show as `webhook` / `push` triggers and in the activity as actor `webhook`. Commits: a Git app always builds its branch tip
- [x] Auto-deploy toggle per app; regenerate deploy token — the webhook is off until turned on; New URL replaces the token, Turn off removes it

### TLS modes
- [x] `http_only` (behind Cloudflare proxy) — per domain, chosen when adding it or switched later (`PATCH /api/domains/{id}`); served on :80 with no certificate and no redirect; verified against Caddy in dev and in production: panel domain `ops.azayr.com` behind Cloudflare (SSL Flexible) on a real VPS (2026-11-11)
- [ ] `dns01` (wildcards; provider credentials)
- [ ] `custom` — upload certificate and key, expiry tracking
- [ ] Domain redirects (`redirect_to`), www / apex

### UI
- [x] Health check settings — app Settings → Health check: type, path, expected status, start period, interval, timeout, failures in a row; checked in a browser (2026-11-14)
- [x] Deployment timeline with per-state timing — above the log of the selected deployment: one bar per step on a shared scale, with its duration, the running step live, the step it failed or was cancelled in marked; the list shows a running deployment's step. Deployments from before have none ("No steps recorded")
- [x] Rollback picker — select a deployment in the Deployments tab, "Roll back to this" with a confirmation in its log panel; rollbacks are marked ↩ in the list and get the usual deploy toasts
- [x] Webhook and auto-deploy settings — Deploy webhook panel in the app's Settings: URL with Copy, how to add it on GitHub, GitLab and Gitea (or a curl line for image apps), a warning when the panel is reached on a local address
- [ ] Certificates page
- [x] Container terminal (WebSocket + xterm.js) — `internal/api/terminal.go`, `internal/executor/terminal.go`, `web/src/components/container-terminal.tsx`; tested in `internal/api/terminal_test.go` and on a real VPS (2026-11-13)

---

## M4 — Databases

Goal: managed Postgres and MariaDB with scheduled S3 backups and restore.

### Instances (`internal/database`)
- [ ] Migration: `db_instances`, `db_databases`, `db_users`, `s3_connections`, `backup_policies`, `backups`
- [ ] Driver interface: provision, databases, users, backup, restore
- [ ] Postgres driver
- [ ] MariaDB driver
- [ ] Version picker, persistent volume, CPU and memory limits
- [ ] Internal-only by default; opt-in public port (confirm against the open question)
- [ ] Start / stop / restart / delete with confirmation
- [ ] Connection strings; inject into an app as env vars

### Databases and users
- [ ] Create / drop databases
- [ ] Create / drop users, grants, password rotation
- [ ] Mirror tables kept in sync with the instance

### Storage connection (`internal/storage`)
- [ ] S3 client
- [ ] External S3 connections CRUD (AWS, R2, others) with a connection test

### Backups
- [ ] Manual backup of an instance or a single database
- [ ] Backup policies: cron and retention
- [ ] Streamed, compressed upload to S3
- [ ] Retention pruning
- [ ] Restore from a `backups` row (into the same or a new database)
- [ ] Backup status, size, and failure reason

### UI
- [ ] Database instances list and create flow
- [ ] Instance page: overview, databases, users, backups, logs, settings
- [ ] S3 connections settings
- [ ] Backup policy editor and restore dialog

---

## M5 — Storage and registry

Goal: optional built-in S3 and image registry.

### Internal S3
- [ ] RustFS stability check; ship behind an "experimental" label if needed (open question)
- [ ] RustFS container lifecycle (start, stop, upgrade, data volume)
- [ ] Buckets and access keys management
- [ ] Routed through Caddy with TLS
- [ ] Usable as a backup target and as the registry backend

### Internal registry (`internal/registry`)
- [ ] Registry container lifecycle, backed by an S3 connection
- [ ] Auth for push and pull
- [ ] Routed through Caddy with TLS
- [ ] Push notification endpoint → auto-deploy on a new tag
- [ ] Repositories and tags browser, delete, garbage collection
- [ ] App source: image from the internal registry

### UI
- [ ] Storage page (buckets, keys, usage)
- [ ] Registry page (repositories, tags, push instructions)

---

## M6 — Insights and alerts

Goal: see what the server is doing and get told when something breaks.

### Metrics (`internal/metrics`)
- [ ] Migration: `metrics_samples`, `notification_channels`, `alert_rules`
- [ ] Docker stats sampler, 10 s samples (CPU, memory, network, disk) — per container; engine-wide counts and storage usage already shown by `internal/dockermetrics` (`/api/docker/metrics`, 15 s refresh, disk scan at most once a minute)
- [ ] Rollups to 1 min and 1 h; prune after 30 days
- [x] Host metrics: CPU, memory, disk, load — plus swap, uptime and network; `internal/hostmetrics`, 5 s samples, ten minutes kept in memory, `/api/host/metrics`; reads `/proc` through a read-only `/host/proc` mount on Linux (installer and dev Compose); on a real VPS (2026-11-13)
- [ ] Metrics query API

### Notifications (`internal/notify`)
- [ ] Channel interface with a test-send
- [ ] Email (SMTP)
- [ ] Slack
- [ ] Discord
- [ ] Telegram
- [ ] Generic webhook
- [ ] Alert rules: event, target, channel
- [ ] Events: deploy started / succeeded / failed, health failing, backup failed, disk high

### More engines
- [ ] MongoDB driver
- [ ] ClickHouse driver

### UI
- [~] Global dashboard — host and Docker cards on the overview page (`web/src/components/host-metrics.tsx`, `docker-metrics.tsx`); no per-app charts or history beyond ten minutes
- [ ] Per-app and per-database charts (Recharts)
- [ ] Notification channels settings
- [ ] Alert rules editor

---

## Cross-cutting

- [ ] Unit tests for store, secret, deploy state machine, Caddy config builder
- [ ] Integration tests against a real Docker daemon
- [ ] End-to-end test: install script on a fresh VM
- [ ] Security review: socket exposure, secrets at rest, webhook token handling, headers
- [ ] Panel self-backup (SQLite file + key) and restore procedure
- [ ] Docs: install, first deploy, backups, updates, recovery CLI
- [x] Release pipeline: versioned image (amd64 + arm64), generated `install.sh`, changelog — push a `vX.Y.Z` tag; `.github/workflows/release.yml` publishes `ghcr.io/brahimoulhaj/paas:X.Y.Z` and `:latest` and a GitHub release with generated notes (v0.1.0 → v0.6.0 published)

## Out of v1

Multiple servers · teams and roles beyond a single owner · Docker Compose stacks · preview environments · database replication.

## Open questions

- [ ] Final product name and install domain
- [ ] License: MIT, Apache 2.0, or AGPL
- [ ] RustFS: stable enough, or "experimental" label
- [ ] Database instances: internal-only by default with an opt-in public port?
- [ ] Docker Compose stacks in v1?
- [x] First Git provider: GitHub App or GitLab token — both: a GitHub App and a GitLab token (2026-11-11)
