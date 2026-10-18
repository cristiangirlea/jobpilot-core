ALTER TABLE filter_presets ADD COLUMN IF NOT EXISTS reject_eligibility_requirements boolean NOT NULL DEFAULT false;
ALTER TABLE filter_presets ADD COLUMN IF NOT EXISTS reject_unlisted_languages boolean NOT NULL DEFAULT false;
