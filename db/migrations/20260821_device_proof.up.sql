ALTER TABLE activations
    ADD COLUMN IF NOT EXISTS device_public_key TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS device_proof_nonces (
    digest TEXT PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_device_proof_nonces_expires_at
    ON device_proof_nonces (expires_at);
