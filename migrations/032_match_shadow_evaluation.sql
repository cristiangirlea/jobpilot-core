-- What a job's extraction would have scored, kept beside the match the rules made; no reader of a match selects it.
ALTER TABLE job_matches ADD COLUMN IF NOT EXISTS shadow_evaluation jsonb;
