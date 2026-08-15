package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/tabloy/keygate/internal/billing"
	"github.com/tabloy/keygate/internal/model"
)

var (
	ErrManualRenewalNotSubscription = errors.New("license is not a manual subscription")
	ErrManualRenewalBlocked         = errors.New("license must be reinstated before renewal")
	ErrSubscriptionExpiryRequired   = errors.New("subscription licenses must have an expiry")
	ErrSubscriptionRecordRequired   = errors.New("subscription record is missing")
)

type ManualRenewalResult struct {
	License            *model.License
	PreviousValidUntil *time.Time
	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time
	QueuedWebhooks     []QueuedWebhook
}

type QueuedWebhook struct {
	Webhook  *model.Webhook
	Delivery *model.WebhookDelivery
}

func (s *Store) CreateSubscription(ctx context.Context, sub *model.Subscription) error {
	if sub.ID == "" {
		sub.ID = newID()
	}
	_, err := s.DB.NewInsert().Model(sub).Exec(ctx)
	return err
}

func (s *Store) FindSubscriptionByID(ctx context.Context, id string) (*model.Subscription, error) {
	sub := new(model.Subscription)
	return sub, s.DB.NewSelect().Model(sub).
		Relation("License").Relation("Plan").
		Where("subscription.id = ?", id).Scan(ctx)
}

func (s *Store) FindSubscriptionByLicense(ctx context.Context, licenseID string) (*model.Subscription, error) {
	sub := new(model.Subscription)
	return sub, s.DB.NewSelect().Model(sub).
		Relation("Plan").
		Where("subscription.license_id = ?", licenseID).
		OrderExpr("subscription.created_at DESC").Limit(1).Scan(ctx)
}

func (s *Store) FindSubscriptionByExternal(ctx context.Context, provider, externalID string) (*model.Subscription, error) {
	sub := new(model.Subscription)
	return sub, s.DB.NewSelect().Model(sub).
		Relation("License").Relation("Plan").
		Where("subscription.payment_provider = ? AND subscription.external_id = ?", provider, externalID).Scan(ctx)
}

func (s *Store) ListSubscriptionsByUser(ctx context.Context, userID string) ([]*model.Subscription, error) {
	var out []*model.Subscription
	err := s.DB.NewSelect().Model(&out).
		Relation("License").Relation("Plan").Relation("Plan.Product").
		Where("subscription.user_id = ?", userID).
		OrderExpr("subscription.created_at DESC").Scan(ctx)
	return out, err
}

func (s *Store) UpdateSubscription(ctx context.Context, sub *model.Subscription, cols ...string) error {
	sub.UpdatedAt = time.Now()
	cols = append(cols, "updated_at")
	_, err := s.DB.NewUpdate().Model(sub).Column(cols...).WherePK().Exec(ctx)
	return err
}

// SetLicenseValidUntil updates the license expiry and, for a manual
// subscription, its latest subscription period end in one transaction.
// Requiring the subscription row prevents the two sources of commercial
// expiry from silently diverging.
func (s *Store) SetLicenseValidUntil(ctx context.Context, licenseID string, validUntil *time.Time, syncSubscription bool) error {
	if syncSubscription && validUntil == nil {
		return ErrSubscriptionExpiryRequired
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var sub *model.Subscription
	if syncSubscription {
		sub = new(model.Subscription)
		if err := tx.NewSelect().Model(sub).
			Where("subscription.license_id = ?", licenseID).
			OrderExpr("subscription.created_at DESC").
			Limit(1).
			For("UPDATE").
			Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSubscriptionRecordRequired
			}
			return err
		}
	}

	now := time.Now()
	if _, err := tx.NewUpdate().Model((*model.License)(nil)).
		Set("valid_until = ?", validUntil).
		Set("updated_at = ?", now).
		Where("id = ?", licenseID).
		Exec(ctx); err != nil {
		return err
	}
	if sub != nil {
		if _, err := tx.NewUpdate().Model((*model.Subscription)(nil)).
			Set("current_period_end = ?", validUntil).
			Set("updated_at = ?", now).
			Where("id = ?", sub.ID).
			Exec(ctx); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// RenewManualSubscription advances one paid calendar period and records the
// license, subscription and audit fact atomically.
func (s *Store) RenewManualSubscription(ctx context.Context, licenseID, actorID string, now time.Time) (*ManualRenewalResult, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	lic := new(model.License)
	if err := tx.NewSelect().Model(lic).
		Where("license.id = ?", licenseID).
		For("UPDATE").
		Scan(ctx); err != nil {
		return nil, err
	}
	plan := new(model.Plan)
	if err := tx.NewSelect().Model(plan).Where("plan.id = ?", lic.PlanID).Scan(ctx); err != nil {
		return nil, err
	}
	lic.Plan = plan
	if lic.Plan == nil || lic.Plan.LicenseType != "subscription" ||
		(lic.Plan.BillingInterval != "month" && lic.Plan.BillingInterval != "year") ||
		lic.PaymentProvider != "" || lic.StripeSubscriptionID != "" {
		return nil, ErrManualRenewalNotSubscription
	}
	if lic.Status == model.StatusSuspended || lic.Status == model.StatusRevoked {
		return nil, ErrManualRenewalBlocked
	}
	switch lic.Status {
	case model.StatusActive, model.StatusPastDue, model.StatusExpired, model.StatusCanceled:
	default:
		return nil, fmt.Errorf("license status %q cannot be renewed", lic.Status)
	}

	previous := lic.ValidUntil
	base := now
	periodStart := now
	if previous != nil && previous.After(now) {
		base = *previous
	}

	sub := new(model.Subscription)
	subErr := tx.NewSelect().Model(sub).
		Where("subscription.license_id = ?", lic.ID).
		OrderExpr("subscription.created_at DESC").
		Limit(1).
		For("UPDATE").
		Scan(ctx)
	if subErr != nil && !errors.Is(subErr, sql.ErrNoRows) {
		return nil, subErr
	}
	if subErr == nil && previous != nil && previous.After(now) && sub.CurrentPeriodStart != nil {
		periodStart = *sub.CurrentPeriodStart
	}

	periodEnd, err := billing.AddPeriod(base, lic.Plan.BillingInterval)
	if err != nil {
		return nil, err
	}
	if _, err := tx.NewUpdate().Model((*model.License)(nil)).
		Set("status = ?", model.StatusActive).
		Set("valid_until = ?", periodEnd).
		Set("canceled_at = NULL").
		Set("past_due_at = NULL").
		Set("updated_at = ?", now).
		Where("id = ?", lic.ID).
		Exec(ctx); err != nil {
		return nil, err
	}

	if errors.Is(subErr, sql.ErrNoRows) {
		sub = &model.Subscription{
			ID: newID(), LicenseID: lic.ID, UserID: lic.UserID, PlanID: lic.PlanID,
			Status: model.StatusActive, CurrentPeriodStart: &periodStart, CurrentPeriodEnd: &periodEnd,
		}
		if _, err := tx.NewInsert().Model(sub).Exec(ctx); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.NewUpdate().Model((*model.Subscription)(nil)).
			Set("plan_id = ?", lic.PlanID).
			Set("status = ?", model.StatusActive).
			Set("current_period_start = ?", periodStart).
			Set("current_period_end = ?", periodEnd).
			Set("cancel_at_period_end = false").
			Set("canceled_at = NULL").
			Set("updated_at = ?", now).
			Where("id = ?", sub.ID).
			Exec(ctx); err != nil {
			return nil, err
		}
	}

	audit := &model.AuditLog{
		ID: newID(), Entity: "license", EntityID: lic.ID, Action: "renewed",
		ActorType: "admin", ActorID: actorID,
		Changes: map[string]any{
			"billing_interval":     lic.Plan.BillingInterval,
			"previous_valid_until": previous,
			"valid_until":          periodEnd,
		},
	}
	if _, err := tx.NewInsert().Model(audit).Exec(ctx); err != nil {
		return nil, err
	}

	var webhooks []*model.Webhook
	if err := tx.NewSelect().Model(&webhooks).
		Where("product_id = ? AND active = true AND ? = ANY(events)", lic.ProductID, "license.renewed").
		Scan(ctx); err != nil {
		return nil, err
	}
	queued := make([]QueuedWebhook, 0, len(webhooks))
	for _, webhook := range webhooks {
		delivery := &model.WebhookDelivery{
			ID: newID(), WebhookID: webhook.ID, Event: "license.renewed", Status: "pending",
			Payload: map[string]any{
				"event":     "license.renewed",
				"timestamp": now.UTC().Format(time.RFC3339),
				"data": map[string]any{
					"license_id":           lic.ID,
					"previous_valid_until": previous,
					"valid_until":          periodEnd,
				},
			},
		}
		if _, err := tx.NewInsert().Model(delivery).Exec(ctx); err != nil {
			return nil, err
		}
		queued = append(queued, QueuedWebhook{Webhook: webhook, Delivery: delivery})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	lic.Status = model.StatusActive
	lic.ValidUntil = &periodEnd
	lic.CanceledAt = nil
	lic.PastDueAt = nil
	return &ManualRenewalResult{
		License: lic, PreviousValidUntil: previous,
		CurrentPeriodStart: periodStart, CurrentPeriodEnd: periodEnd,
		QueuedWebhooks: queued,
	}, nil
}

// ChangeLicensePlan keeps the already-paid period while synchronizing the
// license and its latest subscription to the same plan.
func (s *Store) ChangeLicensePlan(ctx context.Context, licenseID, planID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.NewUpdate().Model((*model.License)(nil)).
		Set("plan_id = ?, updated_at = now()", planID).
		Where("id = ?", licenseID).
		Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`
		UPDATE subscriptions
		SET plan_id = ?, updated_at = now()
		WHERE id = (
			SELECT id FROM subscriptions WHERE license_id = ?
			ORDER BY created_at DESC LIMIT 1
		)
	`, planID, licenseID).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListSubscriptions(ctx context.Context, productID, status string, offset, limit int) ([]*model.Subscription, int, error) {
	q := s.DB.NewSelect().Model((*model.Subscription)(nil)).
		Relation("License").Relation("Plan").Relation("Plan.Product").
		OrderExpr("subscription.created_at DESC")
	if productID != "" {
		q = q.Where("subscription.plan_id IN (SELECT id FROM plans WHERE product_id = ?)", productID)
	}
	if status != "" {
		q = q.Where("subscription.status = ?", status)
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	var out []*model.Subscription
	err = q.Offset(offset).Limit(limit).Scan(ctx, &out)
	return out, total, err
}
