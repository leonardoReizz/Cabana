-- The exact image a service was installed with.
--
-- Stored as a full reference rather than just a tag: it is the unambiguous
-- record of what is actually running, and it stays correct even if a template's
-- repository changes in a later cabana version.
ALTER TABLE services ADD COLUMN IF NOT EXISTS image TEXT NOT NULL DEFAULT '';
