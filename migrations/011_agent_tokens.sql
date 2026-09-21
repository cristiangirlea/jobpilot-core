-- Long-lived, revocable tokens for agents and scripts. A browser login still
-- returns a short-lived signed token; these exist so an MCP host or a script
-- never has to hold the user's password, and so access can be withdrawn
-- without rotating JWT_SECRET for everyone.
CREATE TABLE IF NOT EXISTS agent_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    -- Only the sha256 digest is stored, so a database copy cannot be
    -- replayed against the API.
    token_hash text NOT NULL UNIQUE,
    -- The first characters of the token, for telling two tokens apart in
    -- the UI without being enough to use one.
    token_prefix text NOT NULL DEFAULT '',
    scopes text[] NOT NULL DEFAULT ARRAY['read'],
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at timestamptz,
    revoked_at timestamptz
);

CREATE INDEX IF NOT EXISTS agent_tokens_user_idx ON agent_tokens(user_id, created_at DESC);
