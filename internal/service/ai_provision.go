package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
	"github.com/uptrace/bun"
)

var ErrAIConfigChanged = errors.New("AI configuration changed; preview again")

type AIProvisionItem struct {
	ID       string `json:"id"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason"`
	State    string `json:"state"`
}
type AIProvisionPreview struct {
	Revision int64                `json:"revision"`
	Config   model.AIPlanSettings `json:"config"`
	Items    []AIProvisionItem    `json:"items"`
}
type AIProvisionBatch struct {
	bun.BaseModel  `bun:"table:ai_provision_batches"`
	ID             string            `bun:",pk" json:"id"`
	PlanID         string            `json:"plan_id"`
	ActorID        string            `json:"-"`
	RequestKey     string            `json:"-"`
	ConfigRevision int64             `json:"revision"`
	LicenseIDs     []string          `bun:"license_ids,type:jsonb" json:"-"`
	Results        []AIProvisionItem `bun:"type:jsonb" json:"items"`
	CreatedAt      time.Time         `json:"created_at"`
}

func (s *AIService) ProvisionConfig(ctx context.Context, id string) (*store.AIPlanRecord, error) {
	p, err := s.Store.FindPlanByID(ctx, id)
	if err != nil {
		return nil, err
	}
	product, err := s.Store.FindProductByID(ctx, p.ProductID)
	if err != nil {
		return nil, err
	}
	if product.Slug != "movaura-studio" || product.Type != "desktop" || p.LicenseType != "subscription" || p.GraceDays != 0 {
		return nil, errors.New("AI requires a Movaura subscription with zero grace")
	}
	record, err := s.Store.AIPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg := record.Config
	cfg.Enabled = true
	if err = ValidateAIConfig(cfg); err != nil {
		return nil, err
	}
	binding, err := AIProfileBinding(p)
	if err != nil || binding != record.ProfileBinding {
		return nil, errors.New("AI profile is not configured")
	}
	return record, nil
}
func provisionReason(l *model.License, enrolled bool) string {
	if enrolled {
		return "already_bound"
	}
	if !aiUsable(l, time.Now()) {
		return "license_unavailable"
	}
	return ""
}
func AIProvisionEligible(l *model.License) bool { return aiUsable(l, time.Now()) }
func (s *AIService) PreviewProvision(ctx context.Context, planID string) (*AIProvisionPreview, error) {
	cfg, err := s.ProvisionConfig(ctx, planID)
	if err != nil {
		return nil, err
	}
	var licenses []*model.License
	if err = s.Store.DB.NewSelect().Model(&licenses).Where("plan_id=?", planID).Order("id ASC").Limit(501).Scan(ctx); err != nil {
		return nil, err
	}
	if len(licenses) > 500 {
		return nil, errors.New("batch preview supports up to 500 licenses; use individual provisioning for larger plans")
	}
	result := &AIProvisionPreview{Revision: cfg.Revision, Config: cfg.Config, Items: []AIProvisionItem{}}
	for _, l := range licenses {
		b, e := s.Store.AIBinding(ctx, l.ID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		reason := provisionReason(l, e == nil)
		state := "unbound"
		if e == nil {
			state = b.State
		}
		result.Items = append(result.Items, AIProvisionItem{l.ID, reason == "", reason, state})
	}
	return result, nil
}
func (s *AIService) Provision(ctx context.Context, planID, actor, key string, revision int64, ids []string) (*AIProvisionBatch, error) {
	if key == "" || len(key) > 200 || len(ids) == 0 || len(ids) > 500 {
		return nil, errors.New("valid idempotency key and 1-500 license IDs required")
	}
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return nil, errors.New("duplicate license IDs")
		}
	}
	cfg, err := s.ProvisionConfig(ctx, planID)
	if err != nil {
		return nil, err
	}
	plan, err := s.Store.FindPlanByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if _, err = s.Account(ctx, plan.ProductID); err != nil {
		return nil, err
	}
	result := &AIProvisionBatch{ID: store.NewID(), PlanID: planID, ActorID: actor, RequestKey: key, ConfigRevision: revision, LicenseIDs: ids, Results: []AIProvisionItem{}, CreatedAt: time.Now()}
	err = s.Store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := store.LockAIPlan(ctx, tx, planID); err != nil {
			return err
		}
		var prior AIProvisionBatch
		if e := tx.NewSelect().Model(&prior).Where("plan_id=? AND actor_id=? AND request_key=?", planID, actor, key).Scan(ctx); e == nil {
			a, _ := json.Marshal(prior.LicenseIDs)
			b, _ := json.Marshal(ids)
			if string(a) != string(b) || prior.ConfigRevision != revision {
				return ErrAIConfigChanged
			}
			result = &prior
			return nil
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var fresh int64
		if e := tx.NewRaw("SELECT revision FROM ai_plan_settings WHERE plan_id=?", planID).Scan(ctx, &fresh); e != nil {
			return e
		}
		if cfg.Revision != revision || fresh != revision {
			return ErrAIConfigChanged
		}
		zone, err := s.Store.BillingLocation(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			l := new(model.License)
			if err = tx.NewSelect().Model(l).Where("id=? AND plan_id=?", id, planID).For("UPDATE").Scan(ctx); err != nil {
				return errors.New("license outside selected plan")
			}
			exists, e := tx.NewSelect().Model((*model.AIBinding)(nil)).Where("license_id=?", id).Exists(ctx)
			if e != nil {
				return e
			}
			reason := provisionReason(l, exists)
			item := AIProvisionItem{ID: id, Eligible: reason == "", Reason: reason, State: "skipped"}
			if reason == "" {
				limits := cfg.Config
				limits.Enabled = true
				b := &model.AIBinding{LicenseID: id, ProductID: l.ProductID, Config: limits, ProfileBinding: cfg.ProfileBinding, PlanRevision: revision, Origin: "manual", Generation: 1, State: "pending", RemoteView: map[string]any{}, Anchor: l.ValidFrom, Timezone: zone.String(), UpdatedAt: time.Now()}
				if _, err = tx.NewInsert().Model(b).Exec(ctx); err != nil {
					return err
				}
				if err = store.EnqueueAI(ctx, tx, id, "sync", "manual-provision"); err != nil {
					return err
				}
				item.State = "pending"
			}
			result.Results = append(result.Results, item)
		}
		_, err = tx.NewInsert().Model(result).Exec(ctx)
		return err
	})
	return result, err
}
func (s *AIService) ProvisionBatch(ctx context.Context, planID, id string) (*AIProvisionBatch, error) {
	b := new(AIProvisionBatch)
	if err := s.Store.DB.NewSelect().Model(b).Where("id=? AND plan_id=?", id, planID).Scan(ctx); err != nil {
		return nil, err
	}
	for i := range b.Results {
		if b.Results[i].State == "skipped" {
			continue
		}
		v, e := s.Store.AIBinding(ctx, b.Results[i].ID)
		if errors.Is(e, sql.ErrNoRows) {
			b.Results[i].State = "removed"
		} else if e != nil {
			return nil, e
		} else {
			b.Results[i].State = v.State
		}
	}
	return b, nil
}
