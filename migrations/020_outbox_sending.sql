-- Sending approved outbox entries through the employer's form.
--
-- submit_attempted_at is set just before an entry is posted to the employer,
-- and is never cleared, not even by preparing the entry again: an entry that
-- may have reached the employer is never sent a second time.

ALTER TABLE application_outbox ADD COLUMN IF NOT EXISTS submit_attempted_at timestamptz;

-- The sender takes approved entries oldest first.
CREATE INDEX IF NOT EXISTS application_outbox_approved_idx
    ON application_outbox (approved_at) WHERE status = 'approved';
