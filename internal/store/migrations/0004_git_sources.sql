-- Connections to Git hosts, for private repositories. type is 'github_app' (a GitHub App
-- created from the panel) or 'gitlab' (an access token). secret is encrypted JSON: the
-- app's id, keys and links, or the token.
CREATE TABLE git_sources (
    id         TEXT PRIMARY KEY,
    type       TEXT NOT NULL,
    name       TEXT NOT NULL,
    base_url   TEXT NOT NULL,   -- https://github.com, https://gitlab.com or a self-hosted GitLab
    secret     TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- NULL: the repository is public and fetched without credentials.
ALTER TABLE apps ADD COLUMN git_source_id TEXT REFERENCES git_sources(id);
