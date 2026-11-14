-- A rollback is a deployment of an earlier one's image, without a build: rollback_of is
-- the id of the deployment it returns to; '' for a normal deployment.
ALTER TABLE deployments ADD COLUMN rollback_of TEXT NOT NULL DEFAULT '';
