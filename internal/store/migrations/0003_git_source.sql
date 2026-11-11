-- Apps built from a Git repository. source_type 'git' uses these columns; for 'image'
-- apps they stay empty. The repository is public for now: private repositories add a
-- git_source_id (deploy key or provider) later.

ALTER TABLE apps ADD COLUMN git_repository  TEXT NOT NULL DEFAULT ''; -- https URL
ALTER TABLE apps ADD COLUMN git_branch      TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN builder         TEXT NOT NULL DEFAULT ''; -- dockerfile (more builders later)
ALTER TABLE apps ADD COLUMN build_context   TEXT NOT NULL DEFAULT ''; -- folder inside the repository, '' = root
ALTER TABLE apps ADD COLUMN dockerfile_path TEXT NOT NULL DEFAULT ''; -- relative to the build context

-- The commit a Git deployment built. Its image column holds the tag built from it.
ALTER TABLE deployments ADD COLUMN commit_sha TEXT NOT NULL DEFAULT '';
