-- Reusable application answers, private per user.
--
-- An answer is only reused once approved, so a generated draft can never reach
-- an employer without the user having seen it. role_family and technologies are
-- the reuse keys: they are what makes "the same question for a similar role"
-- resolvable without any model call.

CREATE TABLE IF NOT EXISTS answer_bank (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    question_key        text NOT NULL DEFAULT '',
    question_text       text NOT NULL,
    question_normalized text NOT NULL,
    answer              text NOT NULL,
    role_family         text NOT NULL DEFAULT 'general',
    technologies        text[] NOT NULL DEFAULT '{}',
    source              text NOT NULL DEFAULT 'human',
    approved            boolean NOT NULL DEFAULT false,
    usage_count         integer NOT NULL DEFAULT 0,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    last_used_at        timestamptz
);

-- One answer per (question, role family) per user: re-answering the same
-- question for the same kind of role replaces the previous draft rather than
-- accumulating near-duplicates the user would have to review repeatedly.
CREATE UNIQUE INDEX IF NOT EXISTS answer_bank_question_idx
    ON answer_bank (user_id, question_normalized, role_family);

CREATE INDEX IF NOT EXISTS answer_bank_key_idx
    ON answer_bank (user_id, question_key) WHERE approved;

CREATE INDEX IF NOT EXISTS answer_bank_review_idx
    ON answer_bank (user_id, created_at DESC) WHERE NOT approved;
