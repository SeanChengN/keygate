ALTER TABLE ai_provider_accounts
    ADD COLUMN last_refresh_at TIMESTAMPTZ,
    ADD COLUMN health_error TEXT NOT NULL DEFAULT '',
    ADD COLUMN alert_id UUID;

ALTER TABLE email_queue ADD COLUMN ai_alert_id UUID;
CREATE UNIQUE INDEX email_queue_ai_alert_recipient
    ON email_queue (ai_alert_id, lower(to_addr)) WHERE ai_alert_id IS NOT NULL;
