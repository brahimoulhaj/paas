-- Projects, apps, their domains and deployments, and the audit log.

CREATE TABLE projects (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

-- An app is one container image run as one container. source_type is 'image' for now;
-- Git sources and builders add their own columns later.
CREATE TABLE apps (
    id                    TEXT PRIMARY KEY,
    project_id            TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    name                  TEXT NOT NULL,                    -- slug, unique inside the project
    source_type           TEXT NOT NULL DEFAULT 'image',
    image                 TEXT NOT NULL,                    -- e.g. nginx:1.27 or ghcr.io/org/app:tag
    port                  INTEGER NOT NULL,                 -- port the container listens on
    cpu_limit             REAL NOT NULL DEFAULT 0,          -- cores, 0 = unlimited
    memory_limit_mb       INTEGER NOT NULL DEFAULT 0,       -- MiB, 0 = unlimited
    desired_state         TEXT NOT NULL DEFAULT 'stopped',  -- running | stopped
    current_deployment_id TEXT REFERENCES deployments(id) ON DELETE SET NULL,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    UNIQUE (project_id, name)
);
CREATE INDEX apps_project_id ON apps(project_id);

-- A hostname belongs to exactly one app, panel-wide.
CREATE TABLE domains (
    id         TEXT PRIMARY KEY,
    app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    hostname   TEXT NOT NULL UNIQUE,
    tls_mode   TEXT NOT NULL DEFAULT 'auto',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX domains_app_id ON domains(app_id);

-- One row per deploy attempt. status: queued | running | succeeded | failed.
CREATE TABLE deployments (
    id             TEXT PRIMARY KEY,
    app_id         TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    status         TEXT NOT NULL,
    trigger        TEXT NOT NULL DEFAULT 'manual',
    image          TEXT NOT NULL,              -- the image this attempt deployed
    port           INTEGER NOT NULL DEFAULT 0, -- the app port at the time; the proxy dials this one
    container_id   TEXT NOT NULL DEFAULT '',
    container_name TEXT NOT NULL DEFAULT '',   -- also the container's DNS name on the shared network
    error          TEXT NOT NULL DEFAULT '',
    log_path       TEXT NOT NULL DEFAULT '',   -- relative to the data dir
    started_at     TEXT,
    finished_at    TEXT,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX deployments_app_id ON deployments(app_id, id);

-- Audit log and activity feed: one row per mutating action. Target columns are copies,
-- not references, so the history survives the deletion of what it describes.
CREATE TABLE events (
    id          TEXT PRIMARY KEY,
    user_id     TEXT REFERENCES users(id) ON DELETE SET NULL,
    actor       TEXT NOT NULL DEFAULT '',   -- email at the time, or 'system'
    action      TEXT NOT NULL,              -- e.g. app.create, deployment.succeeded
    target_type TEXT NOT NULL DEFAULT '',
    target_id   TEXT NOT NULL DEFAULT '',
    target_name TEXT NOT NULL DEFAULT '',
    detail      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX events_target ON events(target_type, target_id);
