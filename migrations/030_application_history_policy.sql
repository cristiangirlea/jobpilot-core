-- Application history as a filter rule: warn (default), or exclude companies or
-- roles the user already applied to. The rule is read when a match is read, so
-- an import, a status change and a lookback that runs out need no re-score.

ALTER TABLE filter_presets ADD COLUMN IF NOT EXISTS history_mode text NOT NULL DEFAULT 'warn'
    CHECK (history_mode IN ('warn', 'company', 'role'));
ALTER TABLE filter_presets ADD COLUMN IF NOT EXISTS history_lookback text NOT NULL DEFAULT 'all'
    CHECK (history_lookback IN ('all', 'three_months', 'since'));
ALTER TABLE filter_presets ADD COLUMN IF NOT EXISTS history_cutoff_date date;

-- Every name an imported employer was stated under ("A aka B"), normalized by the API.
ALTER TABLE external_application_history ADD COLUMN IF NOT EXISTS employer_keys text[] NOT NULL DEFAULT '{}';
UPDATE external_application_history SET employer_keys = ARRAY[employer_normalized] WHERE employer_keys = '{}';
CREATE INDEX IF NOT EXISTS external_history_employer_keys ON external_application_history USING gin (employer_keys);
CREATE INDEX IF NOT EXISTS application_events_application_idx ON application_events(application_id);

-- A stored date string that the database cannot read counts as unknown.
CREATE OR REPLACE FUNCTION history_day(raw text) RETURNS date LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
    IF raw IS NULL OR raw = '' THEN
        RETURN NULL;
    END IF;
    IF length(raw) = 10 THEN
        RETURN raw::date;
    END IF;
    RETURN (raw::timestamptz AT TIME ZONE 'UTC')::date;
EXCEPTION WHEN others THEN
    RETURN NULL;
END $$;

-- The first day that still counts. NULL means every day counts.
CREATE OR REPLACE FUNCTION history_cutoff(lookback text, since date, today date) RETURNS date LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE lookback WHEN 'three_months' THEN (today - interval '3 months')::date WHEN 'since' THEN since END
$$;

-- Only a known employer-board address names a role exactly; a title never does.
CREATE OR REPLACE FUNCTION history_posting_key(raw text) RETURNS text LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE
        WHEN raw ~* '^https://(boards|job-boards)\.greenhouse\.io/[^/?#]+/jobs/[0-9]+'
            THEN 'greenhouse:' || lower(substring(raw from '(?i)greenhouse\.io/([^/?#]+/jobs/[0-9]+)'))
        WHEN raw ~* '^https://jobs\.lever\.co/[^/?#]+/[^/?#]+'
            THEN 'lever:' || lower(substring(raw from '(?i)lever\.co/([^/?#]+/[^/?#]+)'))
        WHEN raw ~* '^https://jobs\.ashbyhq\.com/[^/?#]+/[^/?#]+'
            THEN 'ashby:' || lower(substring(raw from '(?i)ashbyhq\.com/([^/?#]+/[^/?#]+)'))
        WHEN raw ~* '^https://jobs\.smartrecruiters\.com/[^/?#]+/[0-9]+'
            THEN 'smartrecruiters:' || lower(substring(raw from '(?i)smartrecruiters\.com/([^/?#]+/[0-9]+)'))
        WHEN raw ~* '^https://[^/?#.]+\.recruitee\.com/o/[^/?#]+'
            THEN 'recruitee:' || lower(substring(raw from '(?i)^https://([^/?#]+/o/[^/?#]+)'))
        ELSE '' END
$$;

DROP VIEW IF EXISTS effective_job_matches;
DROP VIEW IF EXISTS application_history_companies;
DROP VIEW IF EXISTS application_history_imported;
DROP VIEW IF EXISTS application_history_recorded;

-- Imported history that asserts an application: never an alert or a generic receipt.
CREATE VIEW application_history_imported AS
SELECT h.user_id, h.id::text AS fact_id, h.employer_keys AS company_keys, h.canonical_url AS posting_url,
    coalesce(h.record->>'title', '') AS title, history_day(h.record->>'applied_at') AS applied_day,
    jsonb_strip_nulls(jsonb_build_object('id', h.id::text, 'employer', h.record->>'employer', 'title', coalesce(h.record->>'title', ''),
        'requisition', h.requisition, 'canonical_url', h.canonical_url, 'applied_at', coalesce(h.record->>'applied_at', ''),
        'outcome_at', coalesce(h.record->>'outcome_at', ''), 'status', h.record->>'status', 'source', h.record->>'source',
        'evidence_kind', h.record->>'evidence_kind', 'evidence_ref', h.record->>'evidence_ref',
        'cv_label', nullif(h.record->>'cv_version', ''))) AS entry
FROM external_application_history h
WHERE h.record->>'status' <> 'unknown' AND h.record->>'evidence_kind' NOT IN ('alert', 'generic_receipt');

-- Applications recorded here that got as far as applied. A draft that was
-- withdrawn was never made. cv is what the sender read, when it sent one.
-- The entry names the job, never its address: a link is withheld where the
-- job itself is rejected, and the job's own page applies that rule.
CREATE VIEW application_history_recorded AS
SELECT a.user_id, a.id::text AS fact_id, a.job_id, j.company_normalized AS company_key,
    coalesce(nullif(j.employer_url, ''), j.apply_url) AS posting_url, j.title, (made.first_at AT TIME ZONE 'UTC')::date AS applied_day,
    jsonb_strip_nulls(jsonb_build_object('id', a.id::text, 'job_id', a.job_id::text, 'employer', j.company, 'title', j.title,
        'requisition', '', 'canonical_url', '',
        'applied_at', coalesce(to_char(made.first_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), ''),
        'outcome_at', CASE WHEN a.status IN ('interview', 'rejected', 'offer', 'withdrawn')
            THEN to_char(a.status_changed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') ELSE '' END,
        'status', a.status, 'source', 'JobPilot', 'evidence_kind', 'application_event', 'evidence_ref', 'application:' || a.id,
        'cv', (SELECT jsonb_strip_nulls(jsonb_build_object('document_id', o.cv_document_id,
                'filename', (SELECT c.filename FROM cv_documents c WHERE c.id = o.cv_document_id AND c.user_id = o.user_id),
                'version_at', o.sent_cv_updated_at))
            FROM application_outbox o
            WHERE o.user_id = a.user_id AND o.application_id = a.id AND o.status = 'sent' AND o.cv_document_id IS NOT NULL
            LIMIT 1))) AS entry
FROM applications a
JOIN jobs j ON j.id = a.job_id
LEFT JOIN LATERAL (SELECT min(e.created_at) AS first_at FROM application_events e
    WHERE e.application_id = a.id AND e.user_id = a.user_id AND e.to_status IN ('applied', 'interview', 'rejected', 'offer')) made ON true
WHERE a.status IN ('applied', 'interview', 'rejected', 'offer') OR made.first_at IS NOT NULL;

-- One row per user and company name with history, so a list of matches finds
-- the few jobs that have any in one pass instead of a lookup per job.
CREATE VIEW application_history_companies AS
SELECT x.user_id, k.company_key FROM application_history_imported x CROSS JOIN LATERAL unnest(x.company_keys) AS k(company_key)
UNION
SELECT n.user_id, n.company_key FROM application_history_recorded n;

-- Every reader that decides whether a job can still be applied to reads this
-- view; the matcher keeps writing job_matches. A job the user already applied
-- to is never excluded by its own history or by another role's.
CREATE VIEW effective_job_matches AS
SELECT m.id, m.user_id, m.job_id, m.filter_id,
    m.rejected OR coalesce(h.blocked, false) AS rejected,
    m.score,
    m.rejection_reasons || CASE WHEN coalesce(h.blocked, false)
        THEN jsonb_build_array('Application history: you already applied to this ' || CASE h.mode WHEN 'role' THEN 'role' ELSE 'company' END || ', and your filter excludes it.')
        ELSE '[]'::jsonb END AS rejection_reasons,
    m.warnings || CASE WHEN coalesce(h.warned, false) AND NOT coalesce(h.blocked, false)
        THEN jsonb_build_array('Application history: you applied to this ' || CASE WHEN h.same_role THEN 'role' ELSE 'company' END || ' before. Review the earlier applications first.')
        ELSE '[]'::jsonb END AS warnings,
    m.reasons, m.matched_skills, m.missing_skills, m.red_flags, m.breakdown, m.created_at,
    h.report AS application_history
FROM job_matches m
JOIN jobs j ON j.id = m.job_id
LEFT JOIN application_history_companies k ON k.user_id = m.user_id AND k.company_key = j.company_normalized
LEFT JOIN LATERAL (
    SELECT max(e.mode) AS mode,
        coalesce(bool_or(e.blocks) FILTER (WHERE NOT e.own), false) AND NOT coalesce(bool_or(e.own), false) AS blocked,
        coalesce(bool_or(e.in_window) FILTER (WHERE NOT e.own), false) AS warned,
        coalesce(bool_or(e.in_window AND e.relation = 'same_role') FILTER (WHERE NOT e.own), false) AS same_role,
        CASE WHEN count(*) FILTER (WHERE NOT e.own) > 0 THEN jsonb_build_object(
            'mode', max(e.mode), 'lookback', max(e.lookback), 'cutoff', max(e.cutoff),
            'blocked', coalesce(bool_or(e.blocks) FILTER (WHERE NOT e.own), false) AND NOT coalesce(bool_or(e.own), false),
            'entries', jsonb_agg(e.entry ORDER BY e.applied_day DESC NULLS FIRST, e.fact_id) FILTER (WHERE NOT e.own)) END AS report
    FROM (
        SELECT f.fact_id, f.applied_day, p.mode, p.lookback, p.cutoff,
            f.job_id IS NOT DISTINCT FROM m.job_id AS own,
            w.in_window, r.relation,
            w.in_window AND (p.cutoff IS NULL OR f.applied_day IS NOT NULL)
                AND (p.mode = 'company' OR (p.mode = 'role' AND r.relation = 'same_role')) AS blocks,
            f.entry || jsonb_build_object('relation', r.relation, 'title_changed', r.relation = 'same_role' AND f.title <> '' AND lower(f.title) <> lower(j.title),
                'in_window', w.in_window, 'date_unknown', f.applied_day IS NULL) AS entry
        FROM (
            SELECT x.fact_id, NULL::uuid AS job_id, x.posting_url, x.title, x.applied_day, x.entry
            FROM application_history_imported x
            WHERE x.user_id = m.user_id AND x.company_keys @> ARRAY[j.company_normalized]
            UNION ALL
            SELECT n.fact_id, n.job_id, n.posting_url, n.title, n.applied_day, n.entry
            FROM application_history_recorded n
            WHERE n.user_id = m.user_id AND n.company_key = j.company_normalized
        ) f
        CROSS JOIN LATERAL (
            SELECT coalesce(max(fp.history_mode), 'warn') AS mode, coalesce(max(fp.history_lookback), 'all') AS lookback,
                history_cutoff(max(fp.history_lookback), max(fp.history_cutoff_date), (now() AT TIME ZONE 'UTC')::date) AS cutoff,
                history_posting_key(coalesce(nullif(j.employer_url, ''), j.apply_url)) AS job_key
            FROM (SELECT history_mode, history_lookback, history_cutoff_date FROM filter_presets
                WHERE user_id = m.user_id AND active ORDER BY created_at DESC, id LIMIT 1) fp
        ) p
        CROSS JOIN LATERAL (SELECT history_posting_key(f.posting_url) AS fact_key) keys
        CROSS JOIN LATERAL (SELECT CASE
            WHEN keys.fact_key <> '' AND keys.fact_key = p.job_key THEN 'same_role'
            WHEN keys.fact_key <> '' AND p.job_key <> '' AND split_part(keys.fact_key, ':', 1) = split_part(p.job_key, ':', 1) THEN 'different_role'
            ELSE 'uncertain_role' END AS relation) r
        CROSS JOIN LATERAL (SELECT p.cutoff IS NULL OR f.applied_day IS NULL OR f.applied_day >= p.cutoff AS in_window) w
        WHERE k.company_key IS NOT NULL
    ) e
) h ON true;
