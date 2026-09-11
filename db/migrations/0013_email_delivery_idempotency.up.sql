-- Make outbound campaign delivery claimable and retry-safe.
ALTER TABLE campaign_contacts
    DROP CONSTRAINT IF EXISTS campaign_contacts_status_check;

ALTER TABLE campaign_contacts
    ADD CONSTRAINT campaign_contacts_status_check
    CHECK (status IN ('pending','active','sending','paused','completed','replied','bounced','unsubscribed','failed'));

ALTER TABLE campaign_contacts
    ADD COLUMN IF NOT EXISTS send_started_at timestamptz,
    ADD COLUMN IF NOT EXISTS send_worker_id text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_cc_stale_sending
    ON campaign_contacts(campaign_id, send_started_at)
    WHERE status = 'sending';

-- Older rows may have reused Message-IDs. Preserve them while making the
-- new idempotency index safe to apply on an existing production database.
WITH duplicates AS (
    SELECT id, row_number() OVER (PARTITION BY tenant_id, message_id ORDER BY created_at, id) AS n
    FROM email_messages
    WHERE direction = 'out' AND message_id <> ''
), changed AS (
    SELECT id FROM duplicates WHERE n > 1
)
UPDATE email_messages AS em
SET message_id = em.message_id || '-' || left(em.id::text, 8)
FROM changed
WHERE em.id = changed.id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_email_messages_tenant_message_id
    ON email_messages(tenant_id, message_id)
    WHERE direction = 'out' AND message_id <> '';
