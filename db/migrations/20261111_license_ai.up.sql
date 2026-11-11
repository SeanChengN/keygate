-- Optional, new-issuance-only AI provisioning. No existing license backfill.
CREATE TABLE ai_provider_accounts (
    product_id TEXT PRIMARY KEY REFERENCES products(id), enabled BOOLEAN NOT NULL DEFAULT false,
    secret BYTEA NOT NULL, state TEXT NOT NULL DEFAULT 'ready', updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE ai_plan_settings (
    plan_id TEXT PRIMARY KEY REFERENCES plans(id), config JSONB NOT NULL, profile_binding TEXT NOT NULL
);
CREATE TABLE license_ai_bindings (
    license_id TEXT PRIMARY KEY REFERENCES licenses(id), product_id TEXT NOT NULL REFERENCES products(id),
    config JSONB NOT NULL, profile_binding TEXT NOT NULL, remote_id BIGINT NOT NULL DEFAULT 0,
    secret BYTEA, revision BIGINT NOT NULL DEFAULT 0, generation BIGINT NOT NULL DEFAULT 1,
    candidate_id BIGINT NOT NULL DEFAULT 0, candidate_secret BYTEA,
    state TEXT NOT NULL DEFAULT 'pending', last_error TEXT NOT NULL DEFAULT '', remote_view JSONB NOT NULL DEFAULT '{}',
    anchor TIMESTAMPTZ NOT NULL, timezone TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE license_ai_jobs (
    id TEXT PRIMARY KEY, license_id TEXT NOT NULL REFERENCES license_ai_bindings(license_id), action TEXT NOT NULL,
    dedupe_key TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending', phase TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (license_id, dedupe_key)
);
CREATE INDEX license_ai_jobs_pending ON license_ai_jobs(created_at) WHERE state = 'pending';
CREATE FUNCTION enqueue_license_ai_sync() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM license_ai_bindings WHERE license_id = NEW.id) THEN
        INSERT INTO license_ai_jobs(id, license_id, action, dedupe_key)
        VALUES (gen_random_uuid()::text, NEW.id, 'sync', 'sync:' || gen_random_uuid()::text);
        UPDATE license_ai_bindings SET state = 'pending', updated_at = now() WHERE license_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER license_ai_lifecycle AFTER UPDATE OF status, valid_until, plan_id ON licenses
FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status OR OLD.valid_until IS DISTINCT FROM NEW.valid_until OR OLD.plan_id IS DISTINCT FROM NEW.plan_id)
EXECUTE FUNCTION enqueue_license_ai_sync();
