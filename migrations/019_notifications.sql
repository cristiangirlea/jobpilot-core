-- Notifications: one list of everything waiting for the user, private per user.
--
-- A row opens when something starts waiting for the user (an unapproved
-- answer or CV, an outbox entry to review or approve) and is resolved, not
-- deleted, when it stops waiting, so the list doubles as a history. kind is
-- open text and subject_id names the waiting thing in its own table, so a new
-- kind of item needs no schema change. detail carries what a reader needs to
-- show the item without another lookup, including href, the web page where it
-- is resolved.

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
    ON notifications (user_id, created_at, id);

-- notification_open records that a thing waits; opening one already open
-- refreshes its title and detail. notification_resolve records that it no
-- longer waits. Both are what any code adding a kind calls, in the same
-- transaction as the change that causes it.
CREATE OR REPLACE FUNCTION notification_open(p_user uuid, p_kind text, p_subject text, p_title text, p_detail jsonb, p_since timestamptz DEFAULT now())
RETURNS void LANGUAGE sql AS $$
    INSERT INTO notifications (user_id, kind, subject_id, title, detail, created_at, updated_at)
    VALUES (p_user, p_kind, p_subject, p_title, COALESCE(p_detail, '{}'::jsonb), p_since, p_since)
    ON CONFLICT (user_id, kind, subject_id) WHERE resolved_at IS NULL DO UPDATE
    SET title = EXCLUDED.title, detail = EXCLUDED.detail, updated_at = now()
    WHERE notifications.title IS DISTINCT FROM EXCLUDED.title OR notifications.detail IS DISTINCT FROM EXCLUDED.detail;
$$;

CREATE OR REPLACE FUNCTION notification_resolve(p_user uuid, p_kind text, p_subject text)
RETURNS void LANGUAGE sql AS $$
    UPDATE notifications SET resolved_at = now(), updated_at = now()
    WHERE user_id = p_user AND kind = p_kind AND subject_id = p_subject AND resolved_at IS NULL;
$$;

-- The three kinds derived from their own tables. Each has one function that
-- says what its item looks like, used by its trigger and by the backfill
-- below, and a trigger that follows every write, so no code path that changes
-- an approval or an outbox status can leave the list wrong.
CREATE OR REPLACE FUNCTION notification_open_answer(a answer_bank, since timestamptz DEFAULT now())
RETURNS void LANGUAGE sql AS $$
    SELECT notification_open(a.user_id, 'answer', a.id::text,
        'Approve the answer to: ' || a.question_text,
        jsonb_build_object('question', a.question_text, 'answer', a.answer, 'role_family', a.role_family, 'source', a.source, 'href', '/answers'),
        since);
$$;

CREATE OR REPLACE FUNCTION notification_open_cv(c cv_documents, since timestamptz DEFAULT now())
RETURNS void LANGUAGE sql AS $$
    SELECT notification_open(c.user_id, 'cv', c.id::text,
        'Approve the ' || CASE WHEN c.kind = 'tailored' THEN 'tailored CV' ELSE 'CV' END || ': ' || COALESCE(NULLIF(c.label, ''), NULLIF(c.filename, ''), 'untitled'),
        jsonb_build_object('cv_kind', c.kind, 'label', c.label, 'filename', c.filename, 'href', '/cv'),
        since);
$$;

CREATE OR REPLACE FUNCTION notification_open_outbox(o application_outbox, since timestamptz DEFAULT now())
RETURNS void LANGUAGE sql AS $$
    SELECT notification_open(o.user_id, 'outbox', o.id::text,
        CASE WHEN o.status = 'ready' THEN 'Approve' ELSE 'Review' END || ' the application to ' || j.title || ' at ' || j.company,
        jsonb_build_object('status', o.status, 'job_id', o.job_id, 'job_title', j.title, 'company', j.company, 'blockers', o.blockers, 'href', '/outbox'),
        since)
    FROM jobs j WHERE j.id = o.job_id;
$$;

CREATE OR REPLACE FUNCTION notify_answer_bank() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' AND NOT OLD.approved AND (TG_OP = 'DELETE' OR NEW.approved) THEN
        PERFORM notification_resolve(OLD.user_id, 'answer', OLD.id::text);
    END IF;
    IF TG_OP <> 'DELETE' AND NOT NEW.approved THEN
        PERFORM notification_open_answer(NEW);
    END IF;
    RETURN NULL;
END
$$;

CREATE OR REPLACE FUNCTION notify_cv_documents() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' AND NOT OLD.approved AND (TG_OP = 'DELETE' OR NEW.approved) THEN
        PERFORM notification_resolve(OLD.user_id, 'cv', OLD.id::text);
    END IF;
    IF TG_OP <> 'DELETE' AND NOT NEW.approved THEN
        PERFORM notification_open_cv(NEW);
    END IF;
    RETURN NULL;
END
$$;

CREATE OR REPLACE FUNCTION notify_application_outbox() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' AND OLD.status IN ('needs_review', 'ready')
        AND (TG_OP = 'DELETE' OR NEW.status NOT IN ('needs_review', 'ready')) THEN
        PERFORM notification_resolve(OLD.user_id, 'outbox', OLD.id::text);
    END IF;
    IF TG_OP <> 'DELETE' AND NEW.status IN ('needs_review', 'ready') THEN
        PERFORM notification_open_outbox(NEW);
    END IF;
    RETURN NULL;
END
$$;

CREATE OR REPLACE TRIGGER notify_answer_bank AFTER INSERT OR UPDATE OR DELETE ON answer_bank
    FOR EACH ROW EXECUTE FUNCTION notify_answer_bank();
CREATE OR REPLACE TRIGGER notify_cv_documents AFTER INSERT OR UPDATE OR DELETE ON cv_documents
    FOR EACH ROW EXECUTE FUNCTION notify_cv_documents();
CREATE OR REPLACE TRIGGER notify_application_outbox AFTER INSERT OR UPDATE OR DELETE ON application_outbox
    FOR EACH ROW EXECUTE FUNCTION notify_application_outbox();

-- What already waits when this runs, dated from its last change. Opening an
-- item already open changes nothing, so a rerun adds nothing.
SELECT notification_open_answer(a, a.updated_at) FROM answer_bank a
    WHERE NOT a.approved AND NOT EXISTS (SELECT 1 FROM notifications n WHERE n.user_id = a.user_id AND n.kind = 'answer' AND n.subject_id = a.id::text AND n.resolved_at IS NULL);
SELECT notification_open_cv(c, c.updated_at) FROM cv_documents c
    WHERE NOT c.approved AND NOT EXISTS (SELECT 1 FROM notifications n WHERE n.user_id = c.user_id AND n.kind = 'cv' AND n.subject_id = c.id::text AND n.resolved_at IS NULL);
SELECT notification_open_outbox(o, o.updated_at) FROM application_outbox o
    WHERE o.status IN ('needs_review', 'ready') AND NOT EXISTS (SELECT 1 FROM notifications n WHERE n.user_id = o.user_id AND n.kind = 'outbox' AND n.subject_id = o.id::text AND n.resolved_at IS NULL);
