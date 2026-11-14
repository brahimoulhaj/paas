-- How a deploy decides the new container is ready for traffic. One row per app; an app
-- without a row keeps the old behaviour: the container must stay up for a few seconds.
CREATE TABLE app_health_checks (
    app_id          TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    type            TEXT NOT NULL,              -- tcp, http or running
    path            TEXT NOT NULL DEFAULT '',   -- http: request path
    expected_status INTEGER NOT NULL DEFAULT 0, -- http: 0 = any 2xx or 3xx
    interval_s      INTEGER NOT NULL,
    timeout_s       INTEGER NOT NULL,
    retries         INTEGER NOT NULL,           -- failures in a row, after the start period, that fail the deploy
    start_period_s  INTEGER NOT NULL,           -- failures before it do not count; running: how long to stay up
    updated_at      TEXT NOT NULL
);
