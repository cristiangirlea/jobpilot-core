-- Where an approved answer may be reused: on every job ('global'), on jobs of
-- its role family ('role_family', what every answer meant before), or on one
-- job only ('job'). Resolution prefers the narrowest scope that matches.
--
-- job_id names the job a job-scoped answer belongs to, and for other scopes
-- the job a draft was written for, so it can still be narrowed to that job
-- when it is approved. A job that disappears leaves its job-scoped answers
-- matching nothing rather than taking the user's other answers with it.

ALTER TABLE answer_bank ADD COLUMN IF NOT EXISTS scope text;
ALTER TABLE answer_bank ADD COLUMN IF NOT EXISTS job_id uuid REFERENCES jobs(id) ON DELETE SET NULL;

-- Backfill: existing answers keep today's behaviour, reuse by role family.
UPDATE answer_bank SET scope = 'role_family' WHERE scope IS NULL;
ALTER TABLE answer_bank ALTER COLUMN scope SET DEFAULT 'role_family';
ALTER TABLE answer_bank ALTER COLUMN scope SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'answer_bank_scope_check'
                   AND conrelid = 'answer_bank'::regclass) THEN
        ALTER TABLE answer_bank ADD CONSTRAINT answer_bank_scope_check
            CHECK (scope IN ('global', 'role_family', 'job'));
    END IF;
END $$;

-- One answer per question per scope per user. The key is what the scope
-- reuses by, so a role-family answer is still one per (question, family) as
-- before, a global one is one per question, and a job one is one per job.
ALTER TABLE answer_bank ADD COLUMN IF NOT EXISTS scope_key text GENERATED ALWAYS AS (
    CASE scope
        WHEN 'global' THEN 'global'
        WHEN 'job' THEN 'job:' || job_id::text
        ELSE 'family:' || role_family
    END) STORED;

CREATE UNIQUE INDEX IF NOT EXISTS answer_bank_scope_idx
    ON answer_bank (user_id, question_normalized, scope_key);
DROP INDEX IF EXISTS answer_bank_question_idx;
