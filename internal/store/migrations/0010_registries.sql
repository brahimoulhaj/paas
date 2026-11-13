-- Private image registries. server is a registry host (ghcr.io, registry.example.com:5000;
-- docker.io for Docker Hub); secret is the password or token, encrypted with the panel key.
CREATE TABLE registries (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    server     TEXT NOT NULL,
    username   TEXT NOT NULL,
    secret     TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- NULL: the image is public and pulled without credentials.
ALTER TABLE apps ADD COLUMN registry_id TEXT REFERENCES registries(id);
