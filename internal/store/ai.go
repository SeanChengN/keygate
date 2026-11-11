package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/tabloy/keygate/internal/model"
	"github.com/uptrace/bun"
)

type AIPlanRecord struct {
	Config         model.AIPlanSettings `bun:"type:jsonb"`
	ProfileBinding string
}

func (s *Store) AIPlan(ctx context.Context, id string) (*AIPlanRecord, error) {
	v := new(AIPlanRecord)
	err := s.DB.NewSelect().Table("ai_plan_settings").Column("config", "profile_binding").Where("plan_id = ?", id).Scan(ctx, v)
	if errors.Is(err, sql.ErrNoRows) {
		return &AIPlanRecord{}, nil
	}
	return v, err
}

func (s *Store) SaveAIPlan(ctx context.Context, id string, v *AIPlanRecord) error {
	raw, err := json.Marshal(v.Config)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO ai_plan_settings(plan_id, config, profile_binding) VALUES (?, ?::jsonb, ?) ON CONFLICT (plan_id) DO UPDATE SET config = EXCLUDED.config, profile_binding = EXCLUDED.profile_binding`, id, string(raw), v.ProfileBinding)
	return err
}

// issueAI copies defaults only at insert time, in the license transaction.
func (s *Store) issueAI(ctx context.Context, tx bun.IDB, l *model.License) error {
	var settings AIPlanRecord
	err := tx.NewSelect().Table("ai_plan_settings").Column("config", "profile_binding").Where("plan_id = ?", l.PlanID).Scan(ctx, &settings)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !settings.Config.Enabled {
		return nil
	}
	var product model.Product
	if err := tx.NewSelect().Model(&product).Where("id = ?", l.ProductID).Scan(ctx); err != nil {
		return err
	}
	if product.Slug != "movaura-studio" || product.Type != "desktop" {
		return errors.New("AI provisioning product mismatch")
	}
	zone, err := s.BillingLocation(ctx)
	if err != nil {
		return err
	}
	anchor := l.ValidFrom
	if anchor.IsZero() {
		anchor = time.Now()
	}
	b := &model.AIBinding{LicenseID: l.ID, ProductID: l.ProductID, Config: settings.Config, ProfileBinding: settings.ProfileBinding, Generation: 1, State: "pending", RemoteView: map[string]any{}, Anchor: anchor, Timezone: zone.String(), UpdatedAt: time.Now()}
	if _, err := tx.NewInsert().Model(b).Exec(ctx); err != nil {
		return err
	}
	return EnqueueAI(ctx, tx, l.ID, "sync", "issue")
}

func EnqueueAI(ctx context.Context, db bun.IDB, id, action, dedupe string) error {
	j := &model.AIJob{ID: newID(), LicenseID: id, Action: action, DedupeKey: dedupe, State: "pending", CreatedAt: time.Now(), NextAttemptAt: time.Now()}
	_, err := db.NewInsert().Model(j).On("CONFLICT (license_id, dedupe_key) DO NOTHING").Exec(ctx)
	return err
}

func (s *Store) AIBinding(ctx context.Context, id string) (*model.AIBinding, error) {
	b := new(model.AIBinding)
	return b, s.DB.NewSelect().Model(b).Where("license_id = ?", id).Scan(ctx)
}
func (s *Store) AIJobs(ctx context.Context, id string) ([]*model.AIJob, error) {
	var jobs []*model.AIJob
	err := s.DB.NewSelect().Model(&jobs).Where("license_id = ?", id).OrderExpr("created_at DESC").Limit(30).Scan(ctx)
	return jobs, err
}

func (s *Store) AIDeliverable(ctx context.Context, id string) (bool, error) {
	b, err := s.AIBinding(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return b.RemoteID > 0 && len(b.Secret) > 0 && b.Revision > 0, nil
}
