-- A user can report a posting that is no longer available, such as a page
-- that says the role is filled but still answers 200, which the link check
-- cannot see. A report hides the job from its reporter at once. The job closes
-- for everyone when an immediate link check confirms it, or when a second
-- person reports it: the catalog is shared, and one mistaken report must not
-- hide a live job from others.
CREATE TABLE IF NOT EXISTS job_reports (
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, user_id)
);
CREATE INDEX IF NOT EXISTS job_reports_user_idx ON job_reports(user_id);
