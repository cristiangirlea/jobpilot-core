ALTER TABLE application_outbox ADD COLUMN IF NOT EXISTS sent_cv_updated_at timestamptz;
