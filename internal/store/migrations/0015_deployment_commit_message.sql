-- For a Git deployment: the message of the commit it built (at most 2000 characters);
-- '' for image apps and deployments made before this.
ALTER TABLE deployments ADD COLUMN commit_message TEXT NOT NULL DEFAULT '';
