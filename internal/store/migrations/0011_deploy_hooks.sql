-- Deploy webhook of an app. deploy_hook_hash finds the app from the token in the URL;
-- deploy_hook is the token itself, encrypted, so the URL can be shown again. NULL: off.
ALTER TABLE apps ADD COLUMN deploy_hook_hash TEXT;
ALTER TABLE apps ADD COLUMN deploy_hook TEXT;
CREATE UNIQUE INDEX apps_deploy_hook_hash ON apps(deploy_hook_hash);
