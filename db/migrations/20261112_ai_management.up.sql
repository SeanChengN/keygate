ALTER TABLE ai_plan_settings ADD COLUMN revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE license_ai_bindings ADD COLUMN plan_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE license_ai_bindings ADD COLUMN origin TEXT NOT NULL DEFAULT 'issuance';
CREATE TABLE ai_provision_batches (
    id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    request_key TEXT NOT NULL,
    config_revision BIGINT NOT NULL,
    license_ids JSONB NOT NULL,
    results JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(plan_id, actor_id, request_key)
);
