-- Email verification and password reset. An account created from now on
-- starts unverified; every account that exists when this runs was created
-- before verification existed, so it counts as verified from its creation.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at timestamptz;
UPDATE users SET email_verified_at = created_at WHERE email_verified_at IS NULL;

-- Single-use links sent by email. Only the sha256 digest of a link's token
-- is stored, so a database copy cannot verify an address or reset a password.
CREATE TABLE IF NOT EXISTS email_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose text NOT NULL CHECK (purpose IN ('verify', 'reset')),
    token_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at timestamptz
);

CREATE INDEX IF NOT EXISTS email_tokens_user_idx ON email_tokens(user_id, purpose);
