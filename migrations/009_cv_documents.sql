-- Uploaded and generated CVs, private per user.
--
-- The original bytes are kept alongside the extracted text so a CV can always
-- be re-parsed after the parser improves, and so the exact file the user
-- approved is the file that gets sent - never a re-render of it.
--
-- approved is the gate the user asked for: no CV is attached to an application
-- until they have seen the parse and said yes.

CREATE TABLE IF NOT EXISTS cv_documents (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind           text NOT NULL DEFAULT 'base',
    label          text NOT NULL DEFAULT '',
    filename       text NOT NULL DEFAULT '',
    format         text NOT NULL DEFAULT '',
    content_type   text NOT NULL DEFAULT '',
    size_bytes     integer NOT NULL DEFAULT 0,
    data           bytea,
    extracted_text text NOT NULL DEFAULT '',
    parsed         jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Reuse keys, mirroring the answer bank: an approved tailored CV is reused
    -- for the next job with the same role family and a similar stack.
    role_family    text NOT NULL DEFAULT '',
    technologies   text[] NOT NULL DEFAULT '{}',
    approved       boolean NOT NULL DEFAULT false,
    approved_at    timestamptz,
    parent_id      uuid REFERENCES cv_documents(id) ON DELETE SET NULL,
    fingerprint    text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- Re-uploading the identical file replaces its row instead of piling up copies
-- the user would have to review again.
CREATE UNIQUE INDEX IF NOT EXISTS cv_documents_fingerprint_idx
    ON cv_documents (user_id, fingerprint) WHERE fingerprint <> '';

CREATE INDEX IF NOT EXISTS cv_documents_user_idx
    ON cv_documents (user_id, kind, created_at DESC);

CREATE INDEX IF NOT EXISTS cv_documents_reuse_idx
    ON cv_documents (user_id, kind, role_family) WHERE approved;
