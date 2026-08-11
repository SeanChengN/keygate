-- Normalize legacy subscription plans before enforcing the billing contract.
UPDATE plans
SET billing_interval = 'month'
WHERE license_type = 'subscription' AND billing_interval = '';

UPDATE plans
SET billing_interval = ''
WHERE license_type IN ('perpetual', 'trial') AND billing_interval <> '';

ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_license_type_billing_interval_check;
ALTER TABLE plans ADD CONSTRAINT plans_license_type_billing_interval_check CHECK (
    (license_type = 'subscription' AND billing_interval IN ('month', 'year'))
    OR (license_type IN ('perpetual', 'trial') AND billing_interval = '')
);

-- Existing manual subscriptions start from their original license issue time.
UPDATE licenses AS l
SET valid_until = CASE p.billing_interval
        WHEN 'month' THEN l.valid_from + INTERVAL '1 month'
        WHEN 'year' THEN l.valid_from + INTERVAL '1 year'
    END,
    updated_at = now()
FROM plans AS p
WHERE l.plan_id = p.id
  AND p.license_type = 'subscription'
  AND l.valid_until IS NULL;

UPDATE subscriptions AS s
SET plan_id = l.plan_id,
    current_period_start = COALESCE(s.current_period_start, l.valid_from),
    current_period_end = COALESCE(s.current_period_end, l.valid_until),
    updated_at = now()
FROM licenses AS l
WHERE s.license_id = l.id
  AND l.valid_until IS NOT NULL;

UPDATE licenses AS l
SET status = 'expired',
    updated_at = now()
FROM plans AS p
WHERE l.plan_id = p.id
  AND p.license_type = 'subscription'
  AND l.status IN ('active', 'past_due')
  AND l.valid_until IS NOT NULL
  AND now() > l.valid_until + make_interval(days => p.grace_days);

UPDATE subscriptions AS s
SET status = l.status,
    updated_at = now()
FROM licenses AS l
WHERE s.license_id = l.id
  AND s.status <> l.status;
