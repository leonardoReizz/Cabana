-- Installed service instances (a Redis, a Postgres) and their generated
-- credentials.
CREATE TABLE IF NOT EXISTS services (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    -- The catalogue template this was installed from, e.g. "redis".
    template   TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'provisioning',
    -- Cluster-internal address apps reach it on.
    host       TEXT NOT NULL DEFAULT '',
    port       INTEGER NOT NULL DEFAULT 0,
    message    TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Generated at install time and never regenerated: they are also live in the
-- running container, so rotating one means restarting the service.
CREATE TABLE IF NOT EXISTS service_secrets (
    service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    PRIMARY KEY (service_id, key)
);

-- Which apps are wired to which services.
--
-- The injected variable is NOT copied into app_env: it is merged at deploy time
-- from this table. That keeps one source of truth — unlinking removes the
-- variable automatically, and a user editing their env cannot accidentally
-- delete or corrupt a managed connection string.
CREATE TABLE IF NOT EXISTS app_service_links (
    app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    -- Stored rather than re-read from the template, so renaming a variable in a
    -- future template version cannot silently change a running app's env.
    env_var    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, service_id)
);

CREATE INDEX IF NOT EXISTS app_service_links_service_idx ON app_service_links (service_id);
