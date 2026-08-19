ALTER TABLE discovery_sources ADD COLUMN IF NOT EXISTS excluded_search_query text NOT NULL DEFAULT '';

ALTER TABLE discovery_sources DROP CONSTRAINT IF EXISTS discovery_sources_user_id_provider_identifier_search_query_key;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'discovery_sources_source_rule_key') THEN
        ALTER TABLE discovery_sources
            ADD CONSTRAINT discovery_sources_source_rule_key
            UNIQUE(user_id, provider, identifier, search_query, excluded_search_query);
    END IF;
END $$;
