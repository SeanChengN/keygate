ALTER TABLE release_artifacts
    ADD COLUMN artifact_kind TEXT NOT NULL DEFAULT 'application'
    CHECK (artifact_kind IN ('application', 'help-media'));

ALTER TABLE release_artifacts
    DROP CONSTRAINT release_artifacts_release_id_platform_key;

ALTER TABLE release_artifacts
    ADD CONSTRAINT release_artifacts_release_platform_kind_key
    UNIQUE (release_id, platform, artifact_kind);

CREATE INDEX idx_release_artifacts_kind
    ON release_artifacts(artifact_kind);
