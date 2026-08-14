CREATE TABLE customers (
    id                   TEXT PRIMARY KEY,
    kind                 TEXT NOT NULL DEFAULT 'individual'
                         CHECK (kind IN ('individual', 'organization')),
    name                 TEXT NOT NULL,
    primary_email        TEXT NOT NULL,
    phone                TEXT NOT NULL DEFAULT '',
    company              TEXT NOT NULL DEFAULT '',
    notes                TEXT NOT NULL DEFAULT '',
    external_customer_id TEXT NOT NULL DEFAULT '',
    stripe_customer_id   TEXT NOT NULL DEFAULT '',
    archived_at          TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_customers_external_id_unique
    ON customers (external_customer_id) WHERE external_customer_id <> '';
CREATE UNIQUE INDEX idx_customers_stripe_id_unique
    ON customers (stripe_customer_id) WHERE stripe_customer_id <> '';
CREATE INDEX idx_customers_email ON customers (lower(primary_email));
CREATE INDEX idx_customers_archived ON customers (archived_at);

ALTER TABLE licenses
    ADD COLUMN customer_id TEXT REFERENCES customers(id) ON DELETE SET NULL;
CREATE INDEX idx_licenses_customer ON licenses (customer_id);
