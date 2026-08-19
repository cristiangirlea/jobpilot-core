ALTER TABLE discovery_sources DROP CONSTRAINT IF EXISTS discovery_sources_provider_check;
ALTER TABLE discovery_sources ADD CONSTRAINT discovery_sources_provider_check CHECK (provider IN ('remotive','greenhouse','lever','ashby','smartrecruiters','arbeitnow'));
ALTER TABLE filter_presets ADD COLUMN IF NOT EXISTS included_technologies text[] NOT NULL DEFAULT '{}';
ALTER TABLE filter_presets ADD COLUMN IF NOT EXISTS excluded_technologies text[] NOT NULL DEFAULT '{}';

