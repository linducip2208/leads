-- search queue: queued status, crawl/enrich counters, dedupe-safe uniques
ALTER TABLE lead_searches DROP CONSTRAINT IF EXISTS lead_searches_status_check;
ALTER TABLE lead_searches ADD CONSTRAINT lead_searches_status_check
    CHECK (status IN ('queued','running','paused','completed','failed','cancelled'));

ALTER TABLE lead_searches ADD COLUMN IF NOT EXISTS crawled_count int NOT NULL DEFAULT 0;
ALTER TABLE lead_searches ADD COLUMN IF NOT EXISTS enriched_count int NOT NULL DEFAULT 0;

-- allow many companies/contacts without domain/email (partial uniques)
ALTER TABLE companies DROP CONSTRAINT IF EXISTS companies_tenant_id_domain_key;
DROP INDEX IF EXISTS uq_companies_tenant_domain;
CREATE UNIQUE INDEX uq_companies_tenant_domain ON companies(tenant_id, domain) WHERE domain <> '';

ALTER TABLE contacts DROP CONSTRAINT IF EXISTS contacts_tenant_id_email_key;
DROP INDEX IF EXISTS uq_contacts_tenant_email;
CREATE UNIQUE INDEX uq_contacts_tenant_email ON contacts(tenant_id, email) WHERE email <> '';

CREATE INDEX IF NOT EXISTS idx_leads_tenant_company ON leads(tenant_id, company_id);
CREATE INDEX IF NOT EXISTS idx_raw_search_status ON raw_leads(search_id, status);
CREATE INDEX IF NOT EXISTS idx_searches_tenant_status ON lead_searches(tenant_id, status);
