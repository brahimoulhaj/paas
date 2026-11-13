-- How many times a deployment has started. A deployment interrupted by a panel restart
-- starts again from the beginning; this caps how often.
ALTER TABLE deployments ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
