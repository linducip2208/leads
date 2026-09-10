-- team/admin/billing groundwork: trigram support for fuzzy dedupe
DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_trgm;
EXCEPTION WHEN insufficient_privilege OR OTHERS THEN
    RAISE NOTICE 'pg_trgm unavailable; fuzzy dedupe falls back to LIKE';
END
$$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        CREATE INDEX IF NOT EXISTS idx_companies_name_trgm
            ON companies USING gin (lower(name) gin_trgm_ops);
    END IF;
END
$$;
