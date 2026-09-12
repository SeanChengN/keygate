DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM release_artifacts
        WHERE artifact_kind <> 'application'
    ) THEN
        RAISE EXCEPTION 'cannot downgrade while non-application release artifacts exist';
    END IF;
END $$;

DROP INDEX idx_release_artifacts_kind;

ALTER TABLE release_artifacts
    DROP CONSTRAINT release_artifacts_release_platform_kind_key;

ALTER TABLE release_artifacts
    DROP COLUMN artifact_kind;

ALTER TABLE release_artifacts
    ADD CONSTRAINT release_artifacts_release_id_platform_key
    UNIQUE (release_id, platform);
