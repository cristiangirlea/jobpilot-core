CREATE TABLE IF NOT EXISTS discovery_settings (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled boolean NOT NULL DEFAULT false,
    interval_minutes integer NOT NULL DEFAULT 360 CHECK (interval_minutes >= 360),
    auto_prepare boolean NOT NULL DEFAULT true,
    min_score integer NOT NULL DEFAULT 75 CHECK (min_score BETWEEN 0 AND 100),
    max_jobs_per_run integer NOT NULL DEFAULT 100 CHECK (max_jobs_per_run BETWEEN 1 AND 500),
    last_run_at timestamptz,
    next_run_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS discovery_sources (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider IN ('remotive','greenhouse','lever')), name text NOT NULL,
    identifier text NOT NULL DEFAULT '', search_query text NOT NULL DEFAULT '', active boolean NOT NULL DEFAULT true,
    last_crawled_at timestamptz, last_error text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_id, provider, identifier, search_query)
);
CREATE INDEX IF NOT EXISTS discovery_sources_user_idx ON discovery_sources(user_id, active);
CREATE TABLE IF NOT EXISTS discovery_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('running','completed','failed')), jobs_seen integer NOT NULL DEFAULT 0,
    jobs_added integer NOT NULL DEFAULT 0, jobs_rejected integer NOT NULL DEFAULT 0,
    applications_prepared integer NOT NULL DEFAULT 0, duplicates_skipped integer NOT NULL DEFAULT 0,
    error_message text NOT NULL DEFAULT '', started_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz
);
CREATE INDEX IF NOT EXISTS discovery_runs_user_idx ON discovery_runs(user_id, started_at DESC);
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS source_id uuid REFERENCES discovery_sources(id) ON DELETE SET NULL;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS external_id text NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS fingerprint text NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS discovered_at timestamptz;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS partner_apply_url text NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS referral_partner text NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS referral_disclosure text NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS source_provider text NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS source_name text NOT NULL DEFAULT '';
UPDATE jobs j SET source_provider=s.provider,source_name=s.name FROM discovery_sources s WHERE j.source_id=s.id AND (j.source_provider='' OR j.source_name='');
ALTER TABLE jobs ALTER COLUMN user_id DROP NOT NULL;
DROP INDEX IF EXISTS jobs_user_fingerprint_unique;
CREATE UNIQUE INDEX IF NOT EXISTS jobs_fingerprint_unique ON jobs(fingerprint) WHERE fingerprint <> '';
CREATE TABLE IF NOT EXISTS outbound_clicks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE, application_id uuid REFERENCES applications(id) ON DELETE SET NULL,
    destination_url text NOT NULL, referral_partner text NOT NULL DEFAULT '', clicked_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS outbound_clicks_job_idx ON outbound_clicks(job_id, clicked_at DESC);
