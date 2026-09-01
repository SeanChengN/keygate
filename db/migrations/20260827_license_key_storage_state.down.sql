DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM security_state
        WHERE key = 'license_key_storage_mode' AND value = 'ciphertext_only'
    ) THEN
        RAISE EXCEPTION 'cannot downgrade ciphertext_only key storage; restore the verified pre-transition database backup';
    END IF;
END
$$;

DROP TABLE IF EXISTS security_state;
