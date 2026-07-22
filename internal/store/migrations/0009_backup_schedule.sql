-- Per-service backup schedule. 'off', 'daily' or 'weekly', run around the given
-- UTC hour. A coarse schedule rather than raw cron: the choices a person
-- actually makes for a database backup are "how often" and "roughly when".
ALTER TABLE services ADD COLUMN IF NOT EXISTS backup_schedule TEXT NOT NULL DEFAULT 'off';
ALTER TABLE services ADD COLUMN IF NOT EXISTS backup_hour INTEGER NOT NULL DEFAULT 3;

-- How many succeeded backups to keep per service. 0 keeps everything.
ALTER TABLE backup_config ADD COLUMN IF NOT EXISTS retention_count INTEGER NOT NULL DEFAULT 0;

-- Backups of cabana's own database — apps, services, links, secrets. This is
-- the disaster-recovery copy: restore it onto a fresh install and the platform
-- is whole again. Separate from `backups` because it belongs to no service.
CREATE TABLE IF NOT EXISTS state_backups (
    id          TEXT PRIMARY KEY,
    status      TEXT NOT NULL DEFAULT 'running',
    object_key  TEXT NOT NULL,
    size_bytes  BIGINT NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);
