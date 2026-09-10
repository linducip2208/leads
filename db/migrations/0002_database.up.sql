-- lead database: companies, contacts, discovery, leads, segments
CREATE TABLE companies (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name              text NOT NULL,
    legal_name        text NOT NULL DEFAULT '',
    domain            text NOT NULL DEFAULT '',
    website           text NOT NULL DEFAULT '',
    description       text NOT NULL DEFAULT '',
    industry          text NOT NULL DEFAULT '',
    category          text NOT NULL DEFAULT '',
    employee_range    text NOT NULL DEFAULT '',
    revenue_range     text NOT NULL DEFAULT '',
    country           text NOT NULL DEFAULT 'Indonesia',
    province          text NOT NULL DEFAULT '',
    city              text NOT NULL DEFAULT '',
    district          text NOT NULL DEFAULT '',
    address           text NOT NULL DEFAULT '',
    postal_code       text NOT NULL DEFAULT '',
    phone             text NOT NULL DEFAULT '',
    whatsapp          text NOT NULL DEFAULT '',
    linkedin_url      text NOT NULL DEFAULT '',
    instagram_url     text NOT NULL DEFAULT '',
    facebook_url      text NOT NULL DEFAULT '',
    x_url             text NOT NULL DEFAULT '',
    youtube_url       text NOT NULL DEFAULT '',
    technologies      text[] NOT NULL DEFAULT '{}',
    lead_score        int NOT NULL DEFAULT 0,
    owner_id          uuid REFERENCES users(id) ON DELETE SET NULL,
    source            text NOT NULL DEFAULT 'manual',
    external_id       text NOT NULL DEFAULT '',
    last_enriched_at  timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, domain)
);
CREATE INDEX idx_companies_tenant ON companies(tenant_id);
CREATE INDEX idx_companies_tenant_name ON companies(tenant_id, lower(name));
CREATE INDEX idx_companies_tenant_industry ON companies(tenant_id, industry);
CREATE INDEX idx_companies_tenant_city ON companies(tenant_id, city);
CREATE INDEX idx_companies_owner ON companies(owner_id);

CREATE TABLE contacts (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    company_id        uuid REFERENCES companies(id) ON DELETE SET NULL,
    first_name        text NOT NULL DEFAULT '',
    last_name         text NOT NULL DEFAULT '',
    full_name         text NOT NULL DEFAULT '',
    job_title         text NOT NULL DEFAULT '',
    department        text NOT NULL DEFAULT '',
    seniority         text NOT NULL DEFAULT '',
    email             citext NOT NULL DEFAULT '',
    email_status      text NOT NULL DEFAULT 'unknown' CHECK (email_status IN ('unknown','valid','invalid','risky','catch_all','disposable')),
    phone             text NOT NULL DEFAULT '',
    mobile            text NOT NULL DEFAULT '',
    whatsapp          text NOT NULL DEFAULT '',
    linkedin_url      text NOT NULL DEFAULT '',
    confidence_score  int NOT NULL DEFAULT 0,
    lead_score        int NOT NULL DEFAULT 0,
    source            text NOT NULL DEFAULT 'manual',
    source_url        text NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, email)
);
CREATE INDEX idx_contacts_tenant ON contacts(tenant_id);
CREATE INDEX idx_contacts_tenant_company ON contacts(tenant_id, company_id);
CREATE INDEX idx_contacts_tenant_email ON contacts(tenant_id, email);
CREATE INDEX idx_contacts_tenant_phone ON contacts(tenant_id, NULLIF(phone,''));
CREATE INDEX idx_contacts_tenant_name ON contacts(tenant_id, lower(full_name));

CREATE TABLE lead_sources (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid REFERENCES tenants(id) ON DELETE CASCADE,
    slug       text NOT NULL,
    name       text NOT NULL,
    type       text NOT NULL CHECK (type IN ('google_places','crawler','directory','csv','xlsx','manual','api')),
    config     jsonb NOT NULL DEFAULT '{}',
    is_active  boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);

CREATE TABLE lead_searches (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    keyword         text NOT NULL DEFAULT '',
    query           text NOT NULL DEFAULT '',
    industry        text NOT NULL DEFAULT '',
    location        text NOT NULL DEFAULT '',
    country         text NOT NULL DEFAULT 'Indonesia',
    filters         jsonb NOT NULL DEFAULT '{}',
    limit_count     int NOT NULL DEFAULT 1000,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','paused','completed','failed','cancelled')),
    sources_total   int NOT NULL DEFAULT 0,
    sources_done    int NOT NULL DEFAULT 0,
    found_count     int NOT NULL DEFAULT 0,
    saved_count     int NOT NULL DEFAULT 0,
    duplicate_count int NOT NULL DEFAULT 0,
    qualified_count int NOT NULL DEFAULT 0,
    failed_count    int NOT NULL DEFAULT 0,
    pages_crawled   int NOT NULL DEFAULT 0,
    error           text NOT NULL DEFAULT '',
    started_at      timestamptz,
    finished_at     timestamptz,
    duration_ms     bigint NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_searches_tenant_time ON lead_searches(tenant_id, created_at DESC);
CREATE INDEX idx_searches_status ON lead_searches(status);
CREATE INDEX idx_searches_user ON lead_searches(user_id);

CREATE TABLE lead_search_stats (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    search_id  uuid NOT NULL REFERENCES lead_searches(id) ON DELETE CASCADE,
    at         timestamptz NOT NULL DEFAULT now(),
    found      int NOT NULL DEFAULT 0,
    saved      int NOT NULL DEFAULT 0,
    duplicates int NOT NULL DEFAULT 0,
    qualified  int NOT NULL DEFAULT 0,
    failed     int NOT NULL DEFAULT 0,
    pages      int NOT NULL DEFAULT 0
);
CREATE INDEX idx_search_stats_search ON lead_search_stats(search_id, at);

CREATE TABLE saved_searches (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       text NOT NULL,
    params     jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE raw_leads (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    search_id     uuid REFERENCES lead_searches(id) ON DELETE CASCADE,
    source_slug   text NOT NULL DEFAULT '',
    external_id   text NOT NULL DEFAULT '',
    name          text NOT NULL DEFAULT '',
    domain        text NOT NULL DEFAULT '',
    website       text NOT NULL DEFAULT '',
    phone         text NOT NULL DEFAULT '',
    email         text NOT NULL DEFAULT '',
    address       text NOT NULL DEFAULT '',
    city          text NOT NULL DEFAULT '',
    province      text NOT NULL DEFAULT '',
    country       text NOT NULL DEFAULT '',
    industry      text NOT NULL DEFAULT '',
    rating        numeric(3,2),
    reviews       int,
    payload       jsonb NOT NULL DEFAULT '{}',
    status        text NOT NULL DEFAULT 'new' CHECK (status IN ('new','normalized','matched','duplicate','failed')),
    company_id    uuid REFERENCES companies(id) ON DELETE SET NULL,
    duplicate_of  uuid REFERENCES companies(id) ON DELETE SET NULL,
    confidence    int NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_raw_tenant_search ON raw_leads(tenant_id, search_id);
CREATE INDEX idx_raw_tenant_domain ON raw_leads(tenant_id, domain);
CREATE INDEX idx_raw_tenant_extid ON raw_leads(tenant_id, source_slug, external_id);
CREATE INDEX idx_raw_status ON raw_leads(tenant_id, status);

CREATE TABLE leads (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    company_id          uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    primary_contact_id  uuid REFERENCES contacts(id) ON DELETE SET NULL,
    status              text NOT NULL DEFAULT 'new' CHECK (status IN ('new','qualified','hot','contacted','archived')),
    lead_score          int NOT NULL DEFAULT 0,
    source              text NOT NULL DEFAULT 'manual',
    source_search_id    uuid REFERENCES lead_searches(id) ON DELETE SET NULL,
    owner_id            uuid REFERENCES users(id) ON DELETE SET NULL,
    last_activity_at    timestamptz,
    archived_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, company_id)
);
CREATE INDEX idx_leads_tenant_status ON leads(tenant_id, status);
CREATE INDEX idx_leads_tenant_score ON leads(tenant_id, lead_score DESC);
CREATE INDEX idx_leads_tenant_created ON leads(tenant_id, created_at DESC);
CREATE INDEX idx_leads_owner ON leads(owner_id);
CREATE INDEX idx_leads_search ON leads(source_search_id);

CREATE TABLE lead_scores (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    lead_id        uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    score          int NOT NULL,
    breakdown      jsonb NOT NULL DEFAULT '[]',
    calculated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (lead_id)
);
CREATE INDEX idx_lead_scores_tenant ON lead_scores(tenant_id);

CREATE TABLE lead_enrichments (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    company_id  uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    kind        text NOT NULL,
    status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed')),
    data        jsonb NOT NULL DEFAULT '{}',
    error       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX idx_enrichments_tenant_company ON lead_enrichments(tenant_id, company_id, created_at DESC);

CREATE TABLE tags (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    color      text NOT NULL DEFAULT 'indigo',
    UNIQUE (tenant_id, name)
);

CREATE TABLE lead_tags (
    lead_id uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    tag_id  uuid NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (lead_id, tag_id)
);

CREATE TABLE lists (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_lists_tenant ON lists(tenant_id);

CREATE TABLE list_leads (
    list_id uuid NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    lead_id uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    PRIMARY KEY (list_id, lead_id)
);

CREATE TABLE segments (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    filters    jsonb NOT NULL DEFAULT '{}',
    is_dynamic boolean NOT NULL DEFAULT true,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_segments_tenant ON segments(tenant_id);

CREATE TABLE segment_members (
    segment_id uuid NOT NULL REFERENCES segments(id) ON DELETE CASCADE,
    lead_id    uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    added_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (segment_id, lead_id)
);

CREATE TABLE saved_views (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    module     text NOT NULL DEFAULT 'leads',
    name       text NOT NULL,
    filters    jsonb NOT NULL DEFAULT '{}',
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_saved_views_user ON saved_views(tenant_id, user_id, module);

CREATE TABLE icps (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    criteria   jsonb NOT NULL DEFAULT '{}',
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_icps_tenant ON icps(tenant_id);

CREATE TABLE scoring_rules (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    signal     text NOT NULL,
    operator   text NOT NULL DEFAULT 'exists' CHECK (operator IN ('exists','equals','contains','gte','lte','between','in')),
    value      text NOT NULL DEFAULT '',
    weight     int NOT NULL DEFAULT 0,
    is_active  boolean NOT NULL DEFAULT true,
    position   int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_scoring_rules_tenant ON scoring_rules(tenant_id, position);

CREATE TABLE products_services (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    match_rules jsonb NOT NULL DEFAULT '{}',
    is_active   boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_products_tenant ON products_services(tenant_id);

CREATE TABLE lead_opportunities (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    lead_id    uuid NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
    product_id uuid NOT NULL REFERENCES products_services(id) ON DELETE CASCADE,
    reason     text NOT NULL DEFAULT '',
    score      int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (lead_id, product_id)
);
CREATE INDEX idx_opportunities_tenant_lead ON lead_opportunities(tenant_id, lead_id);

CREATE TABLE crawl_jobs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    search_id     uuid REFERENCES lead_searches(id) ON DELETE CASCADE,
    company_id    uuid REFERENCES companies(id) ON DELETE SET NULL,
    url           text NOT NULL,
    status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed','skipped')),
    depth         int NOT NULL DEFAULT 0,
    pages_crawled int NOT NULL DEFAULT 0,
    error         text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz
);
CREATE INDEX idx_crawl_jobs_status ON crawl_jobs(status);
CREATE INDEX idx_crawl_jobs_search ON crawl_jobs(search_id);

CREATE TABLE crawler_metrics (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    at         timestamptz NOT NULL DEFAULT now(),
    window_s   int NOT NULL DEFAULT 60,
    pages      int NOT NULL DEFAULT 0,
    success    int NOT NULL DEFAULT 0,
    failed     int NOT NULL DEFAULT 0,
    avg_ms     int NOT NULL DEFAULT 0
);
CREATE INDEX idx_crawler_metrics_at ON crawler_metrics(at DESC);
