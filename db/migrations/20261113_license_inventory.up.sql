CREATE TABLE license_inventory_batches (
    id TEXT PRIMARY KEY, product_id TEXT NOT NULL, plan_id TEXT NOT NULL,
    actor_id TEXT NOT NULL, request_key TEXT NOT NULL, quantity INTEGER NOT NULL CHECK(quantity BETWEEN 1 AND 500),
    notes TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(actor_id, request_key)
);
ALTER TABLE licenses ADD COLUMN issuance_batch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE licenses ADD COLUMN activation_interval TEXT NOT NULL DEFAULT '';
ALTER TABLE licenses ADD COLUMN activation_timezone TEXT NOT NULL DEFAULT '';
ALTER TABLE licenses ADD COLUMN first_export_id TEXT NOT NULL DEFAULT '';
ALTER TABLE licenses ADD COLUMN first_exported_at TIMESTAMPTZ;
CREATE INDEX licenses_inventory_batch ON licenses(issuance_batch_id) WHERE issuance_batch_id <> '';
CREATE TABLE license_key_exports (
    id TEXT PRIMARY KEY, actor_id TEXT NOT NULL, request_key TEXT NOT NULL,
    license_ids JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(actor_id, request_key)
);
ALTER TABLE licenses DROP CONSTRAINT licenses_status_check;
ALTER TABLE licenses ADD CONSTRAINT licenses_status_check CHECK(status IN ('pending_activation','active','trialing','past_due','canceled','expired','suspended','revoked'));
