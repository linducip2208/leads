-- pipeline v2: richer counters, raw statuses + failure reasons, quality scores,
-- canonical domains, tech evidence, source stats, domain cooldowns, job control
ALTER TABLE lead_searches
    ADD COLUMN IF NOT EXISTS discovered_count int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS companies_created int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS companies_matched int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS contactable_count int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS hot_count int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS filtered_count int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS run_attempt int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS worker_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_heartbeat timestamptz;

ALTER TABLE raw_leads DROP CONSTRAINT IF EXISTS raw_leads_status_check;
ALTER TABLE raw_leads ADD CONSTRAINT raw_leads_status_check
    CHECK (status IN ('discovered','normalized','matched','crawled','enriched','converted','filtered','failed'));
UPDATE raw_leads SET status = 'discovered' WHERE status = 'new';
UPDATE raw_leads SET status = 'matched' WHERE status = 'duplicate';
ALTER TABLE raw_leads
    ADD COLUMN IF NOT EXISTS fail_reason text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS quality int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS source_confidence int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS discovered_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS idx_raw_search_fail ON raw_leads(search_id, fail_reason);
CREATE INDEX IF NOT EXISTS idx_raw_search_source ON raw_leads(search_id, source_slug);

ALTER TABLE companies
    ADD COLUMN IF NOT EXISTS data_quality int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS canonical_domain text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_crawl_at timestamptz;
UPDATE companies SET canonical_domain = domain WHERE canonical_domain = '';
CREATE INDEX IF NOT EXISTS idx_companies_canon ON companies(tenant_id, canonical_domain) WHERE canonical_domain <> '';

ALTER TABLE leads
    ADD COLUMN IF NOT EXISTS data_quality int NOT NULL DEFAULT 0;

ALTER TABLE contacts
    ADD COLUMN IF NOT EXISTS email_kind text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS company_technologies (
    company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    name       text NOT NULL,
    confidence int NOT NULL DEFAULT 50,
    evidence   text NOT NULL DEFAULT '',
    PRIMARY KEY (company_id, name)
);

CREATE TABLE IF NOT EXISTS search_source_stats (
    search_id   uuid NOT NULL REFERENCES lead_searches(id) ON DELETE CASCADE,
    source_slug text NOT NULL,
    candidates  int NOT NULL DEFAULT 0,
    accepted    int NOT NULL DEFAULT 0,
    errors      int NOT NULL DEFAULT 0,
    last_error  text NOT NULL DEFAULT '',
    duration_ms bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (search_id, source_slug)
);

CREATE TABLE IF NOT EXISTS domain_cooldowns (
    domain   text PRIMARY KEY,
    reason   text NOT NULL DEFAULT '',
    until    timestamptz NOT NULL,
    failures int NOT NULL DEFAULT 0
);
