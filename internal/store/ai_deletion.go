package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/tabloy/keygate/internal/model"
	"github.com/uptrace/bun"
	"strings"
	"time"
)

var ErrAIDeletionInProgress = errors.New("AI_DELETE_IN_PROGRESS")

func (s *Store) AIDeletion(ctx context.Context, id string) (*model.AIDeletion, error) {
	d := new(model.AIDeletion)
	err := s.DB.NewSelect().Model(d).Where("license_id=?", id).Scan(ctx)
	return d, err
}
func AIDeleting(ctx context.Context, db bun.IDB, id string) (bool, error) {
	return db.NewSelect().Model((*model.AIDeletion)(nil)).Where("license_id=? AND state <> 'done'", id).Exists(ctx)
}

// ValidateDeletion holds license and subscription rows through the transaction.
func ValidateDeletion(ctx context.Context, tx bun.Tx, id string) (*model.License, error) {
	l := new(model.License)
	if err := tx.NewSelect().Model(l).Where("id=?", id).For("UPDATE").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrLicenseNotFound
		}
		return nil, err
	}
	if l.Status != model.StatusRevoked {
		return nil, ErrLicenseNotRevoked
	}
	if strings.TrimSpace(l.PaymentProvider) != "" || strings.TrimSpace(l.StripeSubscriptionID) != "" {
		return nil, ErrLicensePaymentLinked
	}
	var subscriptions []struct {
		PaymentProvider string
		ExternalID      string
	}
	if err := tx.NewRaw("SELECT coalesce(payment_provider,'') AS payment_provider,coalesce(external_id,'') AS external_id FROM subscriptions WHERE license_id=? FOR UPDATE", id).Scan(ctx, &subscriptions); err != nil {
		return nil, err
	}
	for _, row := range subscriptions {
		if strings.TrimSpace(row.PaymentProvider) != "" || strings.TrimSpace(row.ExternalID) != "" {
			return nil, ErrLicensePaymentLinked
		}
	}
	return l, nil
}
func LockAIDeletion(ctx context.Context, tx bun.Tx, id string) error {
	var locked bool
	if err := tx.NewRaw("SELECT pg_try_advisory_xact_lock(hashtextextended(?,0))", "ai-license:"+id).Scan(ctx, &locked); err != nil {
		return err
	}
	if !locked {
		return ErrAIDeleteBusy
	}
	return nil
}
func (s *Store) SaveAIDeletion(ctx context.Context, d *model.AIDeletion) error {
	d.UpdatedAt = time.Now()
	_, err := s.DB.NewUpdate().Model(d).Column("state", "targets", "last_error_code", "updated_at").WherePK().Exec(ctx)
	return err
}
