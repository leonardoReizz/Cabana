-- Build-time variables, passed to the Dockerfile as --build-arg.
--
-- Deliberately a separate table from app_env, not a flag on it: the two behave
-- differently and conflating them misleads. A build arg is baked into the image
-- and only changes on a rebuild; a runtime variable is read by the process on
-- start. Frameworks that inline configuration at build time (Vite, CRA, and
-- most static site generators) can only be configured through this table.
CREATE TABLE IF NOT EXISTS app_build_args (
    app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    key    TEXT NOT NULL,
    value  TEXT NOT NULL,
    PRIMARY KEY (app_id, key)
);
