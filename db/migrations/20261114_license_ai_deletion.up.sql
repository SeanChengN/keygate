ALTER TABLE ai_provider_accounts ADD COLUMN credential_revision BIGINT NOT NULL DEFAULT 1;

-- Persistent deletion tombstones intentionally have no license FK.
CREATE TABLE license_ai_deletions (
 id TEXT PRIMARY KEY, license_id TEXT NOT NULL UNIQUE, product_id TEXT NOT NULL,
 actor_id TEXT NOT NULL, ip_address TEXT NOT NULL DEFAULT '',
 account_revision BIGINT NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'pending', targets JSONB NOT NULL DEFAULT '[]',
 last_error_code TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX license_ai_deletions_pending ON license_ai_deletions(created_at) WHERE state IN ('pending','running','finalizing');
CREATE FUNCTION guard_license_ai_deletion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM license_ai_deletions WHERE license_id=NEW.id AND state <> 'done') THEN
  RAISE EXCEPTION 'AI_DELETE_IN_PROGRESS' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER license_ai_delete_guard BEFORE UPDATE ON licenses FOR EACH ROW EXECUTE FUNCTION guard_license_ai_deletion();

CREATE FUNCTION guard_subscription_ai_deletion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM license_ai_deletions WHERE license_id=NEW.license_id AND state <> 'done') THEN
  RAISE EXCEPTION 'AI_DELETE_IN_PROGRESS' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER subscription_ai_delete_guard BEFORE INSERT OR UPDATE ON subscriptions FOR EACH ROW EXECUTE FUNCTION guard_subscription_ai_deletion();
