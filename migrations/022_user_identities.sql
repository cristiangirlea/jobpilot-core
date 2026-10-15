-- Sign-in identities from an OpenID Connect provider (Google), one per
-- provider account. A person is found again by the provider's subject, which
-- stays the same when their email address changes.
CREATE TABLE IF NOT EXISTS user_identities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider text NOT NULL,
    subject text NOT NULL,
    email text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, subject)
);

CREATE INDEX IF NOT EXISTS user_identities_user_id_idx ON user_identities(user_id);
