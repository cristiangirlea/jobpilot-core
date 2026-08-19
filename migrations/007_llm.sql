-- Optional AI adapter support. The product must still run with no provider
-- configured, so nothing here is required by the deterministic paths.

-- Completion cache: an identical (provider, model, task, system, prompt) is
-- only ever paid for once.
CREATE TABLE IF NOT EXISTS llm_cache (
    cache_key    text PRIMARY KEY,
    task         text NOT NULL DEFAULT '',
    provider     text NOT NULL DEFAULT '',
    model        text NOT NULL DEFAULT '',
    response     text NOT NULL,
    hits         integer NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS llm_cache_task_idx ON llm_cache (task);
CREATE INDEX IF NOT EXISTS llm_cache_last_used_idx ON llm_cache (last_used_at DESC);
