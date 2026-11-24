DROP INDEX email_queue_ai_alert_recipient;
ALTER TABLE email_queue DROP COLUMN ai_alert_id;
UPDATE ai_provider_accounts SET state = 'attention' WHERE state = 'refresh_uncertain';
ALTER TABLE ai_provider_accounts DROP COLUMN alert_id, DROP COLUMN health_error, DROP COLUMN last_refresh_at;
