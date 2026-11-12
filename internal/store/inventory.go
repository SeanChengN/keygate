package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/tabloy/keygate/internal/billing"
	"github.com/tabloy/keygate/internal/license"
	"github.com/tabloy/keygate/internal/model"
	"github.com/uptrace/bun"
)

var ErrInventoryConflict = errors.New("inventory request conflicts with existing batch")
var ErrInventoryIneligible = errors.New("selected licenses are not unactivated, unexported inventory")

type InventoryBatch struct {
	bun.BaseModel `bun:"table:license_inventory_batches"`
	ID            string    `bun:",pk" json:"id"`
	ProductID     string    `json:"product_id"`
	PlanID        string    `json:"plan_id"`
	ActorID       string    `json:"-"`
	RequestKey    string    `json:"-"`
	Quantity      int       `json:"quantity"`
	Notes         string    `json:"notes"`
	CreatedAt     time.Time `json:"created_at"`
}
type KeyExport struct {
	bun.BaseModel `bun:"table:license_key_exports"`
	ID            string    `bun:",pk" json:"id"`
	ActorID       string    `json:"-"`
	RequestKey    string    `json:"-"`
	LicenseIDs    []string  `bun:"license_ids,type:jsonb" json:"license_ids"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Store) InventoryBatch(ctx context.Context, id string) (*InventoryBatch, error) {
	b := new(InventoryBatch)
	err := s.DB.NewSelect().Model(b).Where("id=?", id).Scan(ctx)
	return b, err
}
func (s *Store) CreateInventory(ctx context.Context, b *InventoryBatch, config *AIPlanRecord) error {
	if b.Quantity < 1 || b.Quantity > 500 {
		return ErrInventoryIneligible
	}
	return s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "stock-batch:"+b.ActorID); err != nil {
			return err
		}
		if err := LockAIPlan(ctx, tx, b.PlanID); err != nil {
			return err
		}
		var prior InventoryBatch
		err := tx.NewSelect().Model(&prior).Where("actor_id=? AND request_key=?", b.ActorID, b.RequestKey).Scan(ctx)
		if err == nil {
			if prior.PlanID != b.PlanID || prior.ProductID != b.ProductID || prior.Quantity != b.Quantity || prior.Notes != b.Notes {
				return ErrInventoryConflict
			}
			*b = prior
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var plan model.Plan
		if err := tx.NewSelect().Model(&plan).Where("id=?", b.PlanID).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		if plan.ProductID != b.ProductID || plan.LicenseType != "subscription" || (plan.BillingInterval != "month" && plan.BillingInterval != "year") || plan.GraceDays != 0 || !plan.Active {
			return ErrInventoryIneligible
		}
		// Re-read the version under the same lock used by plan saves and issuance.
		var current AIPlanRecord
		if err := tx.NewSelect().Table("ai_plan_settings").Column("config", "profile_binding", "revision").Where("plan_id=?", b.PlanID).Scan(ctx, &current); err != nil {
			return err
		}
		if current.Revision != config.Revision || current.ProfileBinding != config.ProfileBinding {
			return ErrInventoryConflict
		}
		zone, err := s.BillingLocation(ctx)
		if err != nil {
			return err
		}
		b.ID = newID()
		b.CreatedAt = time.Now()
		if _, err := tx.NewInsert().Model(b).Exec(ctx); err != nil {
			return err
		}
		for n := 0; n < b.Quantity; n++ {
			l := &model.License{ProductID: b.ProductID, PlanID: b.PlanID, LicenseKey: license.GenerateKey(""), Email: "", Status: model.StatusPendingActivation, ValidFrom: b.CreatedAt, IssuanceBatchID: b.ID, ActivationInterval: plan.BillingInterval, ActivationTimezone: zone.String(), Notes: b.Notes}
			if err := s.prepareLicenseForInsert(l); err != nil {
				return err
			}
			if _, err := tx.NewInsert().Model(l).Exec(ctx); err != nil {
				return err
			}
			binding := &model.AIBinding{LicenseID: l.ID, ProductID: l.ProductID, Config: current.Config, ProfileBinding: current.ProfileBinding, PlanRevision: current.Revision, Origin: "stock", Generation: 1, State: "waiting_activation", RemoteView: map[string]any{}, Anchor: b.CreatedAt, Timezone: zone.String(), UpdatedAt: b.CreatedAt}
			if _, err := tx.NewInsert().Model(binding).Exec(ctx); err != nil {
				return err
			}
		}
		_, err = tx.NewInsert().Model(&model.AuditLog{ID: newID(), Entity: "license_batch", EntityID: b.ID, Action: "created", ActorID: b.ActorID, ActorType: "admin", Changes: map[string]any{"quantity": b.Quantity, "plan_id": b.PlanID}}).Exec(ctx)
		return err
	})
}

// StartInventory commits the first subscription period, device and AI task together.
func (s *Store) StartInventory(ctx context.Context, id string, act *model.Activation) (bool, error) {
	started := false
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "ai-license:"+id); err != nil {
			return err
		}
		var l model.License
		if err := tx.NewSelect().Model(&l).Where("id=?", id).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		if l.Status != model.StatusPendingActivation && l.Status != model.StatusActive {
			return ErrInventoryIneligible
		}
		var prior model.Activation
		err := tx.NewSelect().Model(&prior).Where("license_id=?", id).Scan(ctx)
		if err == nil {
			if prior.Identifier != act.Identifier || prior.DevicePublicKey != act.DevicePublicKey {
				return ErrInventoryConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if l.Status != model.StatusPendingActivation {
			return ErrInventoryConflict
		}
		zone, err := time.LoadLocation(l.ActivationTimezone)
		if err != nil {
			return err
		}
		now := time.Now()
		until, err := billing.AddPeriod(now, l.ActivationInterval, zone)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE licenses SET status='active',valid_from=?,valid_until=?,updated_at=? WHERE id=?", now, until, now, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE license_ai_bindings SET anchor=?,state='pending',updated_at=? WHERE license_id=?", now, now, id); err != nil {
			return err
		}
		sub := &model.Subscription{ID: newID(), LicenseID: id, PlanID: l.PlanID, Status: model.StatusActive, CurrentPeriodStart: &now, CurrentPeriodEnd: &until}
		if _, err = tx.NewInsert().Model(sub).Exec(ctx); err != nil {
			return err
		}
		act.ID = newID()
		act.LicenseID = id
		if _, err = tx.NewInsert().Model(act).Exec(ctx); err != nil {
			return err
		}
		if err = EnqueueAI(ctx, tx, id, "sync", "stock-activation"); err != nil {
			return err
		}
		_, err = tx.NewInsert().Model(&model.AuditLog{ID: newID(), Entity: "license", EntityID: id, Action: "activated", ActorType: "sdk", Changes: map[string]any{"inventory": true}}).Exec(ctx)
		started = true
		return err
	})
	return started, err
}
func (s *Store) ExportRecord(ctx context.Context, id string) (*KeyExport, error) {
	e := new(KeyExport)
	err := s.DB.NewSelect().Model(e).Where("id=?", id).Scan(ctx)
	return e, err
}
func (s *Store) CreateKeyExport(ctx context.Context, e *KeyExport) error {
	if len(e.LicenseIDs) < 1 || len(e.LicenseIDs) > 500 {
		return ErrInventoryIneligible
	}
	sort.Strings(e.LicenseIDs)
	for i := 1; i < len(e.LicenseIDs); i++ {
		if e.LicenseIDs[i] == e.LicenseIDs[i-1] {
			return ErrInventoryIneligible
		}
	}
	return s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Actor lock covers retries with different selections before the unique check.
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "key-export:"+e.ActorID); err != nil {
			return err
		}
		var prior KeyExport
		err := tx.NewSelect().Model(&prior).Where("actor_id=? AND request_key=?", e.ActorID, e.RequestKey).Scan(ctx)
		if err == nil {
			if !reflect.DeepEqual(prior.LicenseIDs, e.LicenseIDs) {
				return ErrInventoryConflict
			}
			*e = prior
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var rows []*model.License
		if err = tx.NewSelect().Model(&rows).Where("id IN (?)", bun.In(e.LicenseIDs)).Order("id").For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		if len(rows) != len(e.LicenseIDs) {
			return ErrInventoryIneligible
		}
		for _, l := range rows {
			if l.Status != model.StatusPendingActivation || l.FirstExportID != "" || l.IssuanceBatchID == "" {
				return fmt.Errorf("%w: %s", ErrInventoryIneligible, l.ID)
			}
			if s.DecryptLicenseKey(l) == "" {
				return errors.New("license key unavailable")
			}
		}
		e.ID = newID()
		e.CreatedAt = time.Now()
		if _, err = tx.NewInsert().Model(e).Exec(ctx); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE licenses SET first_export_id=?,first_exported_at=? WHERE id IN (?)", e.ID, e.CreatedAt, bun.In(e.LicenseIDs)); err != nil {
			return err
		}
		_, err = tx.NewInsert().Model(&model.AuditLog{ID: newID(), Entity: "license_export", EntityID: e.ID, Action: "keys_exported", ActorID: e.ActorID, ActorType: "admin", Changes: map[string]any{"count": len(e.LicenseIDs)}}).Exec(ctx)
		return err
	})
}
