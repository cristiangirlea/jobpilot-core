ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS seniorities text[] NOT NULL DEFAULT '{}';

UPDATE user_profiles
SET seniorities = ARRAY[seniority]
WHERE cardinality(seniorities) = 0 AND trim(seniority) <> '';
