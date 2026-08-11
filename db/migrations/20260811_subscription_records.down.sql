-- Data-only backfill is intentionally irreversible: a backfilled subscription may
-- have been renewed or changed after migration and must not be deleted on downgrade.
SELECT 1;
