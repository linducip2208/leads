-- global system roles are keyed by slug with NULL tenant
CREATE UNIQUE INDEX IF NOT EXISTS idx_roles_global_slug ON roles(slug) WHERE tenant_id IS NULL;
