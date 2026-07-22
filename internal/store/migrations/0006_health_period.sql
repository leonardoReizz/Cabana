-- How often the health check runs. Previously hardcoded at 10s, which made the
-- start delay the only exposed timing knob — an inconsistent half of a feature.
ALTER TABLE apps ADD COLUMN IF NOT EXISTS health_period INTEGER NOT NULL DEFAULT 10;
