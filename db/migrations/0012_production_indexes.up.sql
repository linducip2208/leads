-- Query indexes for tenant-scoped list pages and background workers.
-- Keep this migration additive: it is safe on databases that already have
-- equivalent single-column indexes.
CREATE INDEX IF NOT EXISTS idx_companies_tenant_domain_lookup
    ON companies (tenant_id, domain);
CREATE INDEX IF NOT EXISTS idx_companies_tenant_lower_name_lookup
    ON companies (tenant_id, lower(name));
CREATE INDEX IF NOT EXISTS idx_contacts_tenant_email_lookup
    ON contacts (tenant_id, email);
CREATE INDEX IF NOT EXISTS idx_contacts_tenant_phone_lookup
    ON contacts (tenant_id, NULLIF(phone, ''));
CREATE INDEX IF NOT EXISTS idx_leads_tenant_status_created
    ON leads (tenant_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_leads_tenant_score_id
    ON leads (tenant_id, lead_score DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_raw_leads_tenant_search_status
    ON raw_leads (tenant_id, search_id, status);
CREATE INDEX IF NOT EXISTS idx_lead_searches_tenant_status_created
    ON lead_searches (tenant_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_campaign_contacts_campaign_status_created
    ON campaign_contacts (campaign_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_tenant_assignee_status_due
    ON tasks (tenant_id, assignee_id, status, due_at);
