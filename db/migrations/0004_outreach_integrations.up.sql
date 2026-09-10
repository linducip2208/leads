-- outreach: campaigns, sequences, email accounts, messages, templates, suppression
CREATE TABLE email_accounts (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name         text NOT NULL,
    provider     text NOT NULL DEFAULT 'smtp' CHECK (provider IN ('smtp','gmail','microsoft365','amazon_ses','mailgun','sendgrid','resend')),
    from_name    text NOT NULL DEFAULT '',
    from_email   citext NOT NULL,
    reply_to     citext NOT NULL DEFAULT '',
    smtp_host    text NOT NULL DEFAULT '',
    smtp_port    int NOT NULL DEFAULT 587,
    smtp_encryption text NOT NULL DEFAULT 'starttls' CHECK (smtp_encryption IN ('none','starttls','ssl')),
    smtp_username text NOT NULL DEFAULT '',
    smtp_password_enc bytea,
    daily_limit  int NOT NULL DEFAULT 200,
    hourly_limit int NOT NULL DEFAULT 20,
    is_active    boolean NOT NULL DEFAULT true,
    status       text NOT NULL DEFAULT 'connected' CHECK (status IN ('connected','error','disabled')),
    last_error   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_email_accounts_tenant ON email_accounts(tenant_id);

CREATE TABLE email_templates (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name         text NOT NULL,
    subject      text NOT NULL,
    body         text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_email_templates_tenant ON email_templates(tenant_id);

CREATE TABLE campaigns (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name            text NOT NULL,
    status          text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','scheduled','running','paused','completed','cancelled')),
    audience_type   text NOT NULL DEFAULT 'segment' CHECK (audience_type IN ('segment','list','filter','manual')),
    audience_id     uuid,
    email_account_id uuid REFERENCES email_accounts(id) ON DELETE SET NULL,
    total_contacts  int NOT NULL DEFAULT 0,
    sent_count      int NOT NULL DEFAULT 0,
    open_count      int NOT NULL DEFAULT 0,
    click_count     int NOT NULL DEFAULT 0,
    reply_count     int NOT NULL DEFAULT 0,
    bounce_count    int NOT NULL DEFAULT 0,
    unsubscribe_count int NOT NULL DEFAULT 0,
    scheduled_at    timestamptz,
    started_at      timestamptz,
    completed_at    timestamptz,
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_campaigns_tenant_time ON campaigns(tenant_id, created_at DESC);
CREATE INDEX idx_campaigns_status ON campaigns(status);

CREATE TABLE campaign_steps (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    position    int NOT NULL DEFAULT 0,
    day_offset  int NOT NULL DEFAULT 0,
    kind        text NOT NULL DEFAULT 'email' CHECK (kind IN ('email','wait')),
    wait_days   int NOT NULL DEFAULT 0,
    subject     text NOT NULL DEFAULT '',
    body        text NOT NULL DEFAULT '',
    is_reply_stop boolean NOT NULL DEFAULT true
);
CREATE INDEX idx_campaign_steps_campaign ON campaign_steps(campaign_id, position);

CREATE TABLE campaign_contacts (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    campaign_id   uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    contact_id    uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    lead_id       uuid REFERENCES leads(id) ON DELETE SET NULL,
    status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','active','paused','completed','replied','bounced','unsubscribed','failed')),
    current_step  int NOT NULL DEFAULT 0,
    next_send_at  timestamptz,
    replied_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, contact_id)
);
CREATE INDEX idx_cc_campaign_status ON campaign_contacts(campaign_id, status);
CREATE INDEX idx_cc_next_send ON campaign_contacts(campaign_id, next_send_at) WHERE status IN ('pending','active');

CREATE TABLE email_messages (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    campaign_id   uuid REFERENCES campaigns(id) ON DELETE SET NULL,
    step_id       uuid REFERENCES campaign_steps(id) ON DELETE SET NULL,
    email_account_id uuid REFERENCES email_accounts(id) ON DELETE SET NULL,
    contact_id    uuid REFERENCES contacts(id) ON DELETE SET NULL,
    lead_id       uuid REFERENCES leads(id) ON DELETE SET NULL,
    direction     text NOT NULL DEFAULT 'out' CHECK (direction IN ('out','in')),
    message_id    text NOT NULL DEFAULT '',
    in_reply_to   text NOT NULL DEFAULT '',
    from_email    citext NOT NULL DEFAULT '',
    to_email      citext NOT NULL DEFAULT '',
    subject       text NOT NULL DEFAULT '',
    body_text     text NOT NULL DEFAULT '',
    body_html     text NOT NULL DEFAULT '',
    status        text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','sending','sent','failed','bounced')),
    error         text NOT NULL DEFAULT '',
    sent_at       timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_email_messages_tenant_time ON email_messages(tenant_id, created_at DESC);
CREATE INDEX idx_email_messages_campaign ON email_messages(campaign_id);
CREATE INDEX idx_email_messages_contact ON email_messages(contact_id, created_at DESC);
CREATE INDEX idx_email_messages_status ON email_messages(status) WHERE status IN ('queued','sending');

CREATE TABLE email_events (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    message_id  uuid NOT NULL REFERENCES email_messages(id) ON DELETE CASCADE,
    kind        text NOT NULL CHECK (kind IN ('sent','open','click','reply','bounce','complaint','unsubscribe','failed')),
    meta        jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_email_events_message ON email_events(message_id, created_at);
CREATE INDEX idx_email_events_tenant_kind ON email_events(tenant_id, kind, created_at DESC);

CREATE TABLE suppression_list (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email      citext NOT NULL,
    reason     text NOT NULL DEFAULT 'bounce' CHECK (reason IN ('bounce','complaint','unsubscribe','manual')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, email)
);

CREATE TABLE conversations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    contact_id  uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    subject     text NOT NULL DEFAULT '',
    last_message_at timestamptz NOT NULL DEFAULT now(),
    unread_count int NOT NULL DEFAULT 0,
    status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_conversations_tenant ON conversations(tenant_id, last_message_at DESC);

ALTER TABLE email_messages ADD COLUMN conversation_id uuid REFERENCES conversations(id) ON DELETE SET NULL;
CREATE INDEX idx_email_messages_conversation ON email_messages(conversation_id, created_at);

-- integrations & webhooks
CREATE TABLE integrations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    slug       text NOT NULL,
    name       text NOT NULL,
    config     jsonb NOT NULL DEFAULT '{}',
    status     text NOT NULL DEFAULT 'disconnected' CHECK (status IN ('disconnected','connected','error')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);

CREATE TABLE api_keys (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id     uuid REFERENCES users(id) ON DELETE SET NULL,
    name        text NOT NULL,
    key_prefix  text NOT NULL,
    key_hash    text NOT NULL UNIQUE,
    scopes      text[] NOT NULL DEFAULT '{read}',
    last_used_at timestamptz,
    expires_at  timestamptz,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_api_keys_tenant ON api_keys(tenant_id);

CREATE TABLE webhooks (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    url         text NOT NULL,
    events      text[] NOT NULL DEFAULT '{}',
    secret_enc  bytea,
    is_active   boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_webhooks_tenant ON webhooks(tenant_id);

CREATE TABLE webhook_deliveries (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id  uuid NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event       text NOT NULL,
    payload     jsonb NOT NULL DEFAULT '{}',
    status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','success','failed')),
    response_code int,
    attempts    int NOT NULL DEFAULT 0,
    error       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_webhook_deliveries_webhook ON webhook_deliveries(webhook_id, created_at DESC);

-- ai (optional)
CREATE TABLE ai_providers (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    slug        text NOT NULL,
    name        text NOT NULL,
    base_url    text NOT NULL DEFAULT '',
    model       text NOT NULL DEFAULT '',
    api_key_enc bytea,
    is_default  boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);

CREATE TABLE ai_usage (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    provider   text NOT NULL DEFAULT '',
    model      text NOT NULL DEFAULT '',
    kind       text NOT NULL DEFAULT 'generate',
    prompt_tokens  int NOT NULL DEFAULT 0,
    completion_tokens int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_ai_usage_tenant ON ai_usage(tenant_id, created_at DESC);
