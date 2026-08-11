ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_license_type_billing_interval_check;

-- Backfilled billing facts are intentionally retained on rollback.
