DROP TABLE IF EXISTS admin_recovery_codes;
ALTER TABLE users
    DROP COLUMN IF EXISTS auth_generation,
    DROP COLUMN IF EXISTS password_changed_at,
    DROP COLUMN IF EXISTS password_hash;
