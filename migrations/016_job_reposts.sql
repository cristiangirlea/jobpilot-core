-- Job boards repost an opening under a new link, and the link is part of a
-- job's fingerprint, so each repost used to become another catalog job. A
-- posting recognised as a repost is recorded here against the job it
-- reposts, so later runs find it by fingerprint in one lookup.
CREATE TABLE IF NOT EXISTS job_fingerprint_aliases (
    fingerprint text PRIMARY KEY,
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS job_fingerprint_aliases_job_idx ON job_fingerprint_aliases(job_id);

-- Repost candidates are the same provider's jobs at the same company.
CREATE INDEX IF NOT EXISTS jobs_company_provider_idx ON jobs(company_normalized, source_provider);
