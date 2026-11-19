-- Global catalog data holding the employer's words and the model's reading, with no user data.
CREATE TABLE IF NOT EXISTS job_extractions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    model text NOT NULL,
    prompt_version text NOT NULL,
    text_hash text NOT NULL,
    status text NOT NULL CHECK (status IN ('ok', 'rejected')),
    reason text NOT NULL DEFAULT '',
    extraction jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (job_id, model, prompt_version)
);
