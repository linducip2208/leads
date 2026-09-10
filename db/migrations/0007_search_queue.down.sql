-- revert 0007
DROP INDEX IF EXISTS uq_companies_tenant_domain;
DROP INDEX IF EXISTS uq_contacts_tenant_email;
DROP INDEX IF EXISTS idx_leads_tenant_company;
DROP INDEX IF EXISTS idx_raw_search_status;
DROP INDEX IF EXISTS idx_searches_tenant_status;

ALTER TABLE lead_searches DROP COLUMN IF EXISTS enriched_count;
ALTER TABLE lead_searches DROP COLUMN IF EXISTS crawled_count;

ALTER TABLE lead_searches DROP CONSTRAINT IF EXISTS lead_searches_status_check;
ALTER TABLE lead_searches ADD CONSTRAINT lead_searches_status_check
    CHECK (status IN ('pending','running','paused','completed','failed','cancelled'));

ALTER TABLE companies ADD CONSTRAINT companies_tenant_id_domain_key UNIQUE (tenant_id, domain);
ALTER TABLE contacts ADD CONSTRAINT contacts_tenant_id_email_key UNIQUE (tenant_id, email);
