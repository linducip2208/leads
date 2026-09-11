-- Cover the descending keyset scans used by the main list pages.
CREATE INDEX IF NOT EXISTS idx_companies_tenant_created_keyset
    ON companies (tenant_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_leads_tenant_created_keyset
    ON leads (tenant_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_campaigns_tenant_created_keyset
    ON campaigns (tenant_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_deals_tenant_updated_keyset
    ON deals (tenant_id, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_tenant_status_due
    ON tasks (tenant_id, status, due_at, id);
