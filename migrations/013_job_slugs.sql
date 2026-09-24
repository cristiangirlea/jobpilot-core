-- Jobs are addressed in URLs by a readable name taken from the end of their
-- link and where they came from, such as
-- /jobs/arbeitnow-remote-python-backend-engineer-berlin-100001, instead of a
-- uuid. The API names new jobs as they are stored and names existing ones at
-- startup, so an empty slug only means "not named yet".
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS slug text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS jobs_slug_unique ON jobs(slug) WHERE slug <> '';
