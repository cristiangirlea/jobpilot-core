-- Profile repair proposals: one stored base CV's parse set against the profile,
-- kept with both snapshots so an applied repair can be restored.
-- No foreign key to cv_documents: deleting the CV must not delete the record.
CREATE TABLE IF NOT EXISTS profile_repairs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','applied','rejected','restored')),
    proposal jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS profile_repairs_user_idx ON profile_repairs(user_id, created_at DESC);
