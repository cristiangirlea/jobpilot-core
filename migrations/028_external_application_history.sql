-- Private, immutable history. No foreign key to jobs, applications or outbox.
CREATE TABLE IF NOT EXISTS external_application_history (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 external_key text NOT NULL,
 employer_normalized text NOT NULL,
 requisition text NOT NULL,
 canonical_url text NOT NULL,
 record jsonb NOT NULL,
 imported_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(user_id, external_key)
);
CREATE INDEX IF NOT EXISTS external_history_employer ON external_application_history(user_id, employer_normalized);
CREATE INDEX IF NOT EXISTS external_history_url ON external_application_history(user_id, canonical_url) WHERE canonical_url <> '';
CREATE INDEX IF NOT EXISTS external_history_page ON external_application_history(user_id, id);
