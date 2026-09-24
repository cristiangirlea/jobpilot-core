-- The matcher used to return nil skill lists when every relevant skill matched
-- (or none did), and those were stored as JSON null. The API now serves [] for
-- them, but SQL that walks these arrays with jsonb_array_elements_text fails on
-- a scalar, so the stored rows are repaired as well. Safe to run repeatedly.
UPDATE job_matches SET
    rejection_reasons = CASE WHEN jsonb_typeof(rejection_reasons) = 'null' THEN '[]'::jsonb ELSE rejection_reasons END,
    warnings          = CASE WHEN jsonb_typeof(warnings)          = 'null' THEN '[]'::jsonb ELSE warnings END,
    reasons           = CASE WHEN jsonb_typeof(reasons)           = 'null' THEN '[]'::jsonb ELSE reasons END,
    matched_skills    = CASE WHEN jsonb_typeof(matched_skills)    = 'null' THEN '[]'::jsonb ELSE matched_skills END,
    missing_skills    = CASE WHEN jsonb_typeof(missing_skills)    = 'null' THEN '[]'::jsonb ELSE missing_skills END,
    red_flags         = CASE WHEN jsonb_typeof(red_flags)         = 'null' THEN '[]'::jsonb ELSE red_flags END
WHERE 'null' IN (jsonb_typeof(rejection_reasons), jsonb_typeof(warnings), jsonb_typeof(reasons),
                 jsonb_typeof(matched_skills), jsonb_typeof(missing_skills), jsonb_typeof(red_flags));
