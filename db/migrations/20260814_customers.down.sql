DROP INDEX IF EXISTS idx_licenses_customer;
ALTER TABLE licenses DROP COLUMN IF EXISTS customer_id;
DROP TABLE IF EXISTS customers;
