-- Build output, kept per deploy so it survives the in-memory log buffer
-- rotating, the browser closing, and cabana restarting.
--
-- A separate table rather than a column on deploys: the deploy list is read on
-- every app page load and must not drag potentially megabytes of build output
-- along with it.
CREATE TABLE IF NOT EXISTS deploy_logs (
    deploy_id  TEXT PRIMARY KEY REFERENCES deploys(id) ON DELETE CASCADE,
    content    TEXT NOT NULL,
    -- Set when the build produced more output than cabana stores, so the UI can
    -- say so instead of silently showing a partial log.
    truncated  BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
