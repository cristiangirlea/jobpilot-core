ALTER TABLE discovery_settings ADD COLUMN IF NOT EXISTS quiet_application_days integer NOT NULL DEFAULT 10
    CHECK (quiet_application_days BETWEEN 1 AND 365);

ALTER TABLE applications ADD COLUMN IF NOT EXISTS status_changed_at timestamptz;
UPDATE applications a SET status_changed_at = COALESCE(
    (SELECT max(e.created_at) FROM application_events e
     WHERE e.application_id=a.id AND e.user_id=a.user_id AND e.event_type='status_changed'), a.updated_at)
WHERE status_changed_at IS NULL;
ALTER TABLE applications ALTER COLUMN status_changed_at SET DEFAULT now();
ALTER TABLE applications ALTER COLUMN status_changed_at SET NOT NULL;

CREATE OR REPLACE FUNCTION resolve_quiet_application() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IS DISTINCT FROM NEW.status THEN
        NEW.status_changed_at := now();
        PERFORM notification_resolve(OLD.user_id, 'quiet_application', OLD.id::text);
    END IF;
    RETURN NEW;
END
$$;
CREATE OR REPLACE TRIGGER resolve_quiet_application BEFORE UPDATE OF status ON applications
    FOR EACH ROW EXECUTE FUNCTION resolve_quiet_application();

CREATE TABLE IF NOT EXISTS application_follow_ups (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id uuid NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    body text NOT NULL CHECK (length(btrim(body)) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    slot integer NOT NULL CHECK (slot BETWEEN 1 AND 2),
    UNIQUE (application_id, slot)
);

CREATE OR REPLACE FUNCTION limit_application_follow_ups() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- Lock the parent so concurrent inserts share the same two slots.
    PERFORM 1 FROM applications WHERE id=NEW.application_id FOR UPDATE;
    SELECT COALESCE(max(slot),0)+1 INTO NEW.slot FROM application_follow_ups WHERE application_id=NEW.application_id;
    IF NEW.slot > 2 THEN
        RAISE EXCEPTION 'At most two follow-up drafts are allowed per application.' USING ERRCODE='23514', CONSTRAINT='application_follow_ups_limit';
    END IF;
    RETURN NEW;
END
$$;
CREATE OR REPLACE TRIGGER limit_application_follow_ups BEFORE INSERT ON application_follow_ups
    FOR EACH ROW EXECUTE FUNCTION limit_application_follow_ups();
