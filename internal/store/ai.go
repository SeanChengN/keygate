package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tabloy/keygate/internal/model"
	"github.com/uptrace/bun"
)

type AIPlanRecord struct {
	Config         model.AIPlanSettings `bun:"type:jsonb"`
	ProfileBinding string
	Revision       int64
	Affected       int `bun:"-"`
}

func (s *Store) AIPlan(ctx context.Context, id string) (*AIPlanRecord, error) {
	v := new(AIPlanRecord)
	err := s.DB.NewSelect().Table("ai_plan_settings").Column("config", "profile_binding", "revision").Where("plan_id = ?", id).Scan(ctx, v)
	if errors.Is(err, sql.ErrNoRows) {
		return &AIPlanRecord{}, nil
	}
	return v, err
}

func (s *Store) SaveAIPlan(ctx context.Context, id string, v *AIPlanRecord) error {
	validate := v.Config
	if !validate.Enabled && validate.GroupID == 0 {
		validate.GroupID = 1
	}
	if err := validate.ValidateLimits(); err != nil {
		return err
	}
	raw, err := json.Marshal(v.Config)
	if err != nil {
		return err
	}
	return s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := LockAIPlan(ctx, tx, id); err != nil {
			return err
		}
		if err := tx.NewRaw(`INSERT INTO ai_plan_settings(plan_id, config, profile_binding) VALUES (?, ?::jsonb, ?) ON CONFLICT (plan_id) DO UPDATE SET config = EXCLUDED.config, profile_binding = EXCLUDED.profile_binding, revision = ai_plan_settings.revision + 1 RETURNING revision`, id, string(raw), v.ProfileBinding).Scan(ctx, &v.Revision); err != nil {
			return err
		}
		var ids []string
		if err := tx.NewRaw(`SELECT l.id FROM licenses l JOIN license_ai_bindings b ON b.license_id=l.id WHERE l.plan_id=? ORDER BY l.id FOR UPDATE OF l`, id).Scan(ctx, &ids); err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := v.Config.ValidateLimits(); err != nil {
				return err
			}
			if v.ProfileBinding == "" {
				return errors.New("enrolled licenses require valid AI limits and profile")
			}
		}
		cfg := v.Config
		cfg.Enabled = true
		boundRaw, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		for _, licenseID := range ids {
			if deleting, err := AIDeleting(ctx, tx, licenseID); err != nil {
				return err
			} else if deleting {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE license_ai_bindings SET config=?::jsonb, profile_binding=?, plan_revision=?, state=CASE WHEN (SELECT status FROM licenses WHERE id=license_id)='pending_activation' THEN 'waiting_activation' WHEN state='uncertain' THEN state ELSE 'pending' END, updated_at=now() WHERE license_id=?`, string(boundRaw), v.ProfileBinding, v.Revision, licenseID); err != nil {
				return err
			}
			var status string
			if err := tx.NewSelect().Table("licenses").Column("status").Where("id=?", licenseID).Scan(ctx, &status); err != nil {
				return err
			}
			if status != model.StatusPendingActivation {
				if err := EnqueueAI(ctx, tx, licenseID, "sync", fmt.Sprintf("plan:%d", v.Revision)); err != nil {
					return err
				}
			}
		}
		v.Affected = len(ids)
		return nil
	})
}

func LockAIPlan(ctx context.Context, db bun.IDB, id string) error {
	_, err := db.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "ai-plan:"+id)
	return err
}

// issueAI copies defaults only at insert time, in the license transaction.
func (s *Store) issueAI(ctx context.Context, tx bun.IDB, l *model.License) error {
	if err := LockAIPlan(ctx, tx, l.PlanID); err != nil {
		return err
	}
	var settings AIPlanRecord
	err := tx.NewSelect().Table("ai_plan_settings").Column("config", "profile_binding", "revision").Where("plan_id = ?", l.PlanID).Scan(ctx, &settings)
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
	b := &model.AIBinding{LicenseID: l.ID, ProductID: l.ProductID, Config: settings.Config, ProfileBinding: settings.ProfileBinding, PlanRevision: settings.Revision, Origin: "issuance", Generation: 1, State: "pending", RemoteView: map[string]any{}, Anchor: anchor, Timezone: zone.String(), UpdatedAt: time.Now()}
	if _, err := tx.NewInsert().Model(b).Exec(ctx); err != nil {
		return err
	}
	return EnqueueAI(ctx, tx, l.ID, "sync", "issue")
}

func EnqueueAI(ctx context.Context, db bun.IDB, id, action, dedupe string) error {
	if deleting, err := AIDeleting(ctx, db, id); err != nil {
		return err
	} else if deleting {
		return ErrAIDeletionInProgress
	}
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
