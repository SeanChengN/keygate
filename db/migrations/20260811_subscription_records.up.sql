-- Historical hand-issued subscription licenses may predate the subscriptions table.
-- Create the missing row after valid_until has been backfilled by the period migration.
INSERT INTO subscriptions (
    id,
    license_id,
    user_id,
    plan_id,
    status,
    payment_provider,
    external_id,
    current_period_start,
    current_period_end,
    metadata,
    created_at,
    updated_at
)
SELECT
    gen_random_uuid()::text,
    l.id,
    l.user_id,
    l.plan_id,
    l.status,
    l.payment_provider,
    CASE l.payment_provider
        WHEN 'stripe' THEN COALESCE(l.stripe_subscription_id, '')
        WHEN 'paypal' THEN COALESCE(l.paypal_subscription_id, '')
        ELSE ''
    END,
    l.valid_from,
    l.valid_until,
    jsonb_build_object('backfilled_by', '20260811_subscription_records'),
    l.created_at,
    now()
FROM licenses AS l
JOIN plans AS p ON p.id = l.plan_id
WHERE p.license_type = 'subscription'
  AND l.valid_until IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM subscriptions AS s WHERE s.license_id = l.id);
