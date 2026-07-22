-- v0.3: replicas, configurable health checks and rollback.

ALTER TABLE apps ADD COLUMN IF NOT EXISTS replicas INTEGER NOT NULL DEFAULT 1;

-- Health check configuration. 'tcp' asks only whether something accepts a
-- connection; 'http' additionally requires a 2xx from a path, which is what
-- catches an app that listens but is broken.
ALTER TABLE apps ADD COLUMN IF NOT EXISTS health_type TEXT NOT NULL DEFAULT 'tcp';
ALTER TABLE apps ADD COLUMN IF NOT EXISTS health_path TEXT NOT NULL DEFAULT '/';
-- Grace period before the first check. An app with slow startup needs this
-- raised, or Kubernetes kills it before it ever becomes ready.
ALTER TABLE apps ADD COLUMN IF NOT EXISTS health_initial_delay INTEGER NOT NULL DEFAULT 3;

-- The image this deploy produced. Recorded rather than recomputed so a rollback
-- refers to exactly what ran, not to what the naming scheme would produce today.
ALTER TABLE deploys ADD COLUMN IF NOT EXISTS image TEXT NOT NULL DEFAULT '';

-- Set when a deploy is a rollback, naming the deploy it restored. Makes the
-- history self-explanatory instead of showing an unexplained repeat.
ALTER TABLE deploys ADD COLUMN IF NOT EXISTS rolled_back_from TEXT;
