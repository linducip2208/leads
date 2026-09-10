-- crm: pipelines, deals, tasks, activities, notes
CREATE TABLE pipelines (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_pipelines_tenant ON pipelines(tenant_id);

CREATE TABLE pipeline_stages (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pipeline_id uuid NOT NULL REFERENCES pipelines(id) ON DELETE CASCADE,
    name        text NOT NULL,
    position    int NOT NULL DEFAULT 0,
    kind        text NOT NULL DEFAULT 'open' CHECK (kind IN ('open','won','lost')),
    probability int NOT NULL DEFAULT 0
);
CREATE INDEX idx_stages_pipeline ON pipeline_stages(pipeline_id, position);

CREATE TABLE deals (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    pipeline_id       uuid NOT NULL REFERENCES pipelines(id) ON DELETE CASCADE,
    stage_id          uuid NOT NULL REFERENCES pipeline_stages(id) ON DELETE CASCADE,
    company_id        uuid REFERENCES companies(id) ON DELETE SET NULL,
    contact_id        uuid REFERENCES contacts(id) ON DELETE SET NULL,
    lead_id           uuid REFERENCES leads(id) ON DELETE SET NULL,
    title             text NOT NULL,
    value             numeric(14,2) NOT NULL DEFAULT 0,
    currency          text NOT NULL DEFAULT 'IDR',
    probability       int NOT NULL DEFAULT 0,
    expected_close_at date,
    owner_id          uuid REFERENCES users(id) ON DELETE SET NULL,
    products          text[] NOT NULL DEFAULT '{}',
    status            text NOT NULL DEFAULT 'open' CHECK (status IN ('open','won','lost')),
    closed_at         timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_deals_tenant_stage ON deals(tenant_id, stage_id);
CREATE INDEX idx_deals_tenant_owner ON deals(tenant_id, owner_id);
CREATE INDEX idx_deals_company ON deals(company_id);

CREATE TABLE deal_activities (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    deal_id    uuid NOT NULL REFERENCES deals(id) ON DELETE CASCADE,
    kind       text NOT NULL DEFAULT 'note' CHECK (kind IN ('created','stage_changed','note','email','call','meeting','file','won','lost')),
    body       text NOT NULL DEFAULT '',
    meta       jsonb NOT NULL DEFAULT '{}',
    user_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_deal_activities_deal ON deal_activities(deal_id, created_at DESC);

CREATE TABLE tasks (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind         text NOT NULL DEFAULT 'general' CHECK (kind IN ('call','email','whatsapp','meeting','follow_up','general')),
    title        text NOT NULL,
    body         text NOT NULL DEFAULT '',
    due_at       timestamptz,
    priority     text NOT NULL DEFAULT 'medium' CHECK (priority IN ('low','medium','high','urgent')),
    status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open','completed','cancelled')),
    lead_id      uuid REFERENCES leads(id) ON DELETE CASCADE,
    deal_id      uuid REFERENCES deals(id) ON DELETE CASCADE,
    assignee_id  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    completed_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_tasks_tenant_status ON tasks(tenant_id, status);
CREATE INDEX idx_tasks_assignee ON tasks(assignee_id, status);
CREATE INDEX idx_tasks_due ON tasks(tenant_id, due_at);
CREATE INDEX idx_tasks_lead ON tasks(lead_id);

CREATE TABLE activities (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind        text NOT NULL,
    subject     text NOT NULL DEFAULT '',
    body        text NOT NULL DEFAULT '',
    meta        jsonb NOT NULL DEFAULT '{}',
    lead_id     uuid REFERENCES leads(id) ON DELETE CASCADE,
    company_id  uuid REFERENCES companies(id) ON DELETE CASCADE,
    contact_id  uuid REFERENCES contacts(id) ON DELETE CASCADE,
    deal_id     uuid REFERENCES deals(id) ON DELETE CASCADE,
    user_id     uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_activities_tenant_time ON activities(tenant_id, created_at DESC);
CREATE INDEX idx_activities_lead ON activities(lead_id, created_at DESC);
CREATE INDEX idx_activities_company ON activities(company_id, created_at DESC);

CREATE TABLE notes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    body        text NOT NULL,
    lead_id     uuid REFERENCES leads(id) ON DELETE CASCADE,
    company_id  uuid REFERENCES companies(id) ON DELETE CASCADE,
    contact_id  uuid REFERENCES contacts(id) ON DELETE CASCADE,
    deal_id     uuid REFERENCES deals(id) ON DELETE CASCADE,
    user_id     uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_notes_lead ON notes(lead_id, created_at DESC);
