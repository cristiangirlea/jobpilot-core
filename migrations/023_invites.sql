-- Invite codes for sign-up. With REGISTRATION=invite (the default), creating
-- an account needs one; an admin (ADMIN_EMAILS) creates them in the web app.
-- A code is used once: claiming it and creating the account happen in one
-- transaction, so a failed sign-up does not spend it.
CREATE TABLE IF NOT EXISTS invites (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    created_by uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Who the invite is for, as the admin wrote it; never shown to anyone else.
    note text NOT NULL DEFAULT '',
    -- Only the sha256 digest is stored, like agent tokens, so a database copy
    -- cannot be used to sign up.
    code_hash text NOT NULL UNIQUE,
    code_prefix text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    used_by uuid REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS invites_created_by_idx ON invites(created_by, created_at DESC);
