DROP INDEX IF EXISTS idx_email_messages_tenant_message_id;
DROP INDEX IF EXISTS idx_cc_stale_sending;
UPDATE campaign_contacts SET status='pending', send_started_at=NULL, send_worker_id=''
WHERE status='sending';
ALTER TABLE campaign_contacts
    DROP COLUMN IF EXISTS send_worker_id,
    DROP COLUMN IF EXISTS send_started_at;
ALTER TABLE campaign_contacts
    DROP CONSTRAINT IF EXISTS campaign_contacts_status_check;
ALTER TABLE campaign_contacts
    ADD CONSTRAINT campaign_contacts_status_check
    CHECK (status IN ('pending','active','paused','completed','replied','bounced','unsubscribed','failed'));
