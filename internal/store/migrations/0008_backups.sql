-- Where backups are shipped. A singleton: one destination for the whole install,
-- set in the dashboard. The boolean primary key pinned to true is the standard
-- trick for a single-row table.
CREATE TABLE IF NOT EXISTS backup_config (
    id         BOOLEAN PRIMARY KEY DEFAULT true CHECK (id),
    endpoint   TEXT NOT NULL,
    region     TEXT NOT NULL DEFAULT 'auto',
    bucket     TEXT NOT NULL,
    access_key TEXT NOT NULL,
    -- Stored like every other credential cabana holds. A database dump exposes
    -- it, which is the same exposure as service passwords already carry.
    secret_key TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS backups (
    id           TEXT PRIMARY KEY,
    service_id   TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    -- Denormalised so a listing reads without joining, and survives in the row
    -- even as the service changes.
    service_name TEXT NOT NULL,
    template     TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'running',
    object_key   TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    error        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS backups_service_created_idx ON backups (service_id, created_at DESC);
