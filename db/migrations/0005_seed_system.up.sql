-- system seed: permissions, plans
INSERT INTO permissions (slug, description) VALUES
    ('tenant.manage', 'Manage tenant settings and white label'),
    ('team.manage', 'Invite and manage members and roles'),
    ('billing.manage', 'Manage plan, billing and credits'),
    ('integration.manage', 'Manage integrations, API keys and webhooks'),
    ('lead.read', 'View leads, companies and contacts'),
    ('lead.create', 'Create leads, companies and contacts'),
    ('lead.update', 'Update leads, companies and contacts'),
    ('lead.delete', 'Delete and archive leads'),
    ('lead.export', 'Export leads and contacts'),
    ('search.create', 'Run lead searches'),
    ('search.manage', 'Manage all searches in workspace'),
    ('segment.manage', 'Manage segments and lists'),
    ('campaign.create', 'Create campaigns and sequences'),
    ('campaign.launch', 'Launch and pause campaigns'),
    ('deal.manage', 'Manage pipeline and deals'),
    ('task.manage', 'Manage tasks'),
    ('ai.use', 'Use AI features'),
    ('admin.platform', 'Full platform administration')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO plans (slug, name, description, price_monthly, currency, limits, is_active, is_public, position) VALUES
    ('free', 'Free', 'For trying LeadForge with a small list', 0, 'USD',
     '{"users":2,"searches":3,"leads":1000,"enrichment":200,"exports":2,"api":false,"campaign":false,"ai":false,"credits":1000}',
     true, true, 1),
    ('starter', 'Starter', 'Solo founders and small teams getting started', 49, 'USD',
     '{"users":3,"searches":25,"leads":25000,"enrichment":2000,"exports":20,"api":true,"campaign":true,"ai":false,"credits":25000}',
     true, true, 2),
    ('professional', 'Professional', 'Growing sales teams that need scale', 149, 'USD',
     '{"users":10,"searches":100,"leads":150000,"enrichment":20000,"exports":100,"api":true,"campaign":true,"ai":true,"credits":150000}',
     true, true, 3),
    ('business', 'Business', 'High-volume prospecting with advanced controls', 349, 'USD',
     '{"users":25,"searches":500,"leads":1000000,"enrichment":100000,"exports":500,"api":true,"campaign":true,"ai":true,"credits":1000000}',
     true, true, 4),
    ('enterprise', 'Enterprise', 'Custom limits, SSO and priority support', 0, 'USD',
     '{"users":100,"searches":5000,"leads":10000000,"enrichment":1000000,"exports":5000,"api":true,"campaign":true,"ai":true,"credits":10000000}',
     true, true, 5)
ON CONFLICT (slug) DO NOTHING;
