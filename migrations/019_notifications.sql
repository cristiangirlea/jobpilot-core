-- Notifications: one list of everything waiting for the user, private per user.
--
-- A row opens when something starts waiting for the user (an unapproved
-- answer or CV, an outbox entry to review or approve) and is resolved, not
-- deleted, when it stops waiting, so the list doubles as a history. kind is
-- open text and subject_id names the waiting thing in its own table, so a new
-- kind of item needs no schema change. detail carries what a reader needs to
-- show the item without another lookup.

CREATE TABLE IF NOT EXISTS notifications (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        text NOT NULL,
    subject_id  text NOT NULL,
    title       text NOT NULL,
    detail      jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz
);

-- At most one open row per waiting thing; once resolved, the same thing can
-- start waiting again under a new row.
CREATE UNIQUE INDEX IF NOT EXISTS notifications_open_idx
    ON notifications (user_id, kind, subject_id) WHERE resolved_at IS NULL;

CREATE INDEX IF NOT EXISTS notifications_user_idx
    ON notifications (user_id, created_at DESC);
