-- platform: tenants, users, rbac, sessions, plans, notifications, audit
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE tenants (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    slug        text NOT NULL UNIQUE,
    status      text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','cancelled')),
    plan_id     uuid,
    settings    jsonb NOT NULL DEFAULT '{}',
    whitelabel  jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          uuid REFERENCES tenants(id) ON DELETE CASCADE,
    email              citext NOT NULL UNIQUE,
    password_hash      text NOT NULL,
    name               text NOT NULL,
    phone              text,
    is_super_admin     boolean NOT NULL DEFAULT false,
    status             text NOT NULL DEFAULT 'active' CHECK (status IN ('active','invited','suspended')),
    email_verified_at  timestamptz,
    last_login_at      timestamptz,
    settings           jsonb NOT NULL DEFAULT '{}',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_users_tenant ON users(tenant_id);

CREATE TABLE roles (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid REFERENCES tenants(id) ON DELETE CASCADE,
    slug        text NOT NULL,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    is_system   boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);
CREATE INDEX idx_roles_tenant ON roles(tenant_id);

CREATE TABLE permissions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT ''
);

CREATE TABLE role_permissions (
    role_id       uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id uuid NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE user_roles (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);
CREATE INDEX idx_user_roles_role ON user_roles(role_id);

CREATE TABLE sessions (
    id           text PRIMARY KEY,              -- sha256 of session token
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id    uuid REFERENCES tenants(id) ON DELETE CASCADE,
    ip           text NOT NULL DEFAULT '',
    user_agent   text NOT NULL DEFAULT '',
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

CREATE TABLE password_reset_tokens (
    email      citext NOT NULL,
    token_hash text PRIMARY KEY,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_prt_email ON password_reset_tokens(email);

CREATE TABLE email_verification_tokens (
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text PRIMARY KEY,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plans (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug          text NOT NULL UNIQUE,
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    price_monthly numeric(12,2) NOT NULL DEFAULT 0,
    currency      text NOT NULL DEFAULT 'USD',
    limits        jsonb NOT NULL DEFAULT '{}',
    is_active     boolean NOT NULL DEFAULT true,
    is_public     boolean NOT NULL DEFAULT true,
    position      int NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE subscriptions (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            uuid NOT NULL UNIQUE REFERENCES tenants(id) ON DELETE CASCADE,
    plan_id              uuid NOT NULL REFERENCES plans(id),
    status               text NOT NULL DEFAULT 'active' CHECK (status IN ('trialing','active','past_due','cancelled')),
    current_period_start timestamptz NOT NULL DEFAULT now(),
    current_period_end   timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE credit_transactions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('grant','purchase','usage','adjustment','refund')),
    amount        int NOT NULL,
    balance_after int NOT NULL DEFAULT 0,
    description   text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_credit_tx_tenant ON credit_transactions(tenant_id, created_at);

CREATE TABLE usage_events (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    kind       text NOT NULL CHECK (kind IN ('search','crawl','page','enrichment','verification','email','ai','export','api')),
    quantity   int NOT NULL DEFAULT 1,
    meta       jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_usage_tenant_kind_time ON usage_events(tenant_id, kind, created_at);

CREATE TABLE notifications (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid REFERENCES tenants(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       text NOT NULL,
    title      text NOT NULL,
    body       text NOT NULL DEFAULT '',
    link       text NOT NULL DEFAULT '',
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_user ON notifications(user_id, created_at DESC);

CREATE TABLE audit_logs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid REFERENCES tenants(id) ON DELETE SET NULL,
    user_id     uuid REFERENCES users(id) ON DELETE SET NULL,
    action      text NOT NULL,
    entity_type text NOT NULL DEFAULT '',
    entity_id   text NOT NULL DEFAULT '',
    ip          text NOT NULL DEFAULT '',
    meta        jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_tenant_time ON audit_logs(tenant_id, created_at DESC);
