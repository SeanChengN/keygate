ALTER TABLE products ADD COLUMN inventory_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE products ADD COLUMN package_ai_enabled BOOLEAN NOT NULL DEFAULT false;
UPDATE products SET inventory_enabled = true, package_ai_enabled = true
WHERE slug = 'movaura-studio' AND type = 'desktop';

ALTER TABLE plans ADD COLUMN billing_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_billing_interval_check;
ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_license_type_billing_interval_check;
ALTER TABLE plans ADD CONSTRAINT plans_license_type_billing_interval_check CHECK (
    (license_type = 'subscription' AND billing_interval IN ('month', 'year') AND billing_days = 0)
    OR (license_type = 'subscription' AND billing_interval = 'day' AND billing_days BETWEEN 1 AND 365
        AND grace_days = 0 AND stripe_price_id = '')
    OR (license_type IN ('perpetual', 'trial') AND billing_interval = '' AND billing_days = 0)
);

ALTER TABLE licenses ADD COLUMN activation_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE licenses ADD COLUMN inventory_ai BOOLEAN NOT NULL DEFAULT false;
UPDATE licenses SET inventory_ai = true WHERE issuance_batch_id <> '';
ALTER TABLE license_inventory_batches ADD COLUMN with_ai BOOLEAN NOT NULL DEFAULT false;
UPDATE license_inventory_batches SET with_ai = true;

CREATE UNIQUE INDEX entitlements_one_ai_profile
    ON entitlements(plan_id) WHERE feature IN ('studio.ai_profile', 'license.ai_profile');
