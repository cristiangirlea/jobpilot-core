-- A posting closes when its link starts returning 404 or 410, or when it drops
-- out of an employer board that lists every open job. A closed job stays in
-- the catalog for the record, but leaves recommendations and can no longer be
-- prepared for. last_checked_at paces the background link check.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS closed_at timestamptz;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS closed_reason text NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS last_seen_at timestamptz;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS last_checked_at timestamptz;
CREATE INDEX IF NOT EXISTS jobs_link_check_idx ON jobs(last_checked_at NULLS FIRST) WHERE closed_at IS NULL;
