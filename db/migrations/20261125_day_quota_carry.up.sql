ALTER TABLE license_ai_bindings ADD COLUMN quota_carry DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (quota_carry >= 0);
