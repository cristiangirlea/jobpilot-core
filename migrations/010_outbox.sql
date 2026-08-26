-- The outbox: an application composed in full, held until the user releases it.
--
-- Nothing is ever sent straight from composition. An entry carries the CV, the
-- letter, and every resolved form answer, so the user reviews exactly what an
-- employer would receive. blockers records why an entry is not yet sendable, in
-- the user's own terms.

CREATE TABLE IF NOT EXISTS application_outbox (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    job_id              uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    application_id      uuid REFERENCES applications(id) ON DELETE SET NULL,
    status              text NOT NULL DEFAULT 'needs_review',
    channel             text NOT NULL DEFAULT 'manual',
    ats_provider        text NOT NULL DEFAULT '',
    cv_document_id      uuid REFERENCES cv_documents(id) ON DELETE SET NULL,
    cv_decision         text NOT NULL DEFAULT '',
    cover_letter        text NOT NULL DEFAULT '',
    cover_letter_source text NOT NULL DEFAULT '',
    form                jsonb NOT NULL DEFAULT '{}'::jsonb,
    answers             jsonb NOT NULL DEFAULT '[]'::jsonb,
    blockers            text[] NOT NULL DEFAULT '{}',
    notices             text[] NOT NULL DEFAULT '{}',
    needs_review        integer NOT NULL DEFAULT 0,
    error               text NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    approved_at         timestamptz,
    sent_at             timestamptz
);

-- One entry per job per user: recomposing refreshes it rather than queueing a
-- second application to the same posting.
CREATE UNIQUE INDEX IF NOT EXISTS application_outbox_job_idx
    ON application_outbox (user_id, job_id);

CREATE INDEX IF NOT EXISTS application_outbox_status_idx
    ON application_outbox (user_id, status, created_at DESC);

-- Added after the table shipped; the migration runner reruns every file.
ALTER TABLE application_outbox ADD COLUMN IF NOT EXISTS notices text[] NOT NULL DEFAULT '{}';
