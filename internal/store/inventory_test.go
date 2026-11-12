package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/tabloy/keygate/internal/model"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type uncertainConnector struct {
	driver.Connector
	fail *atomic.Bool
}

func (c uncertainConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return uncertainConn{Conn: conn, fail: c.fail}, nil
}

type uncertainConn struct {
	driver.Conn
	fail *atomic.Bool
}

func (c uncertainConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return uncertainTx{Tx: tx, fail: c.fail}, nil
}

type uncertainTx struct {
	driver.Tx
	fail *atomic.Bool
}

func (tx uncertainTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if tx.fail.Swap(false) {
		return errors.New("synthetic COMMIT reply lost")
	}
	return nil
}
func TestInventoryAtomicExportActivationAndDelete(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated database required")
	}
	s, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.RunMigrations("../../db/migrations"))
	p := &model.Product{Name: "Inventory synthetic", Slug: "inventory-" + NewID(), Type: "desktop"}
	must(s.CreateProduct(ctx, p))
	plan := &model.Plan{ProductID: p.ID, Name: "Stock", Slug: NewID(), LicenseType: "subscription", LicenseModel: "standard", BillingInterval: "month", MaxActivations: 1, GraceDays: 0, Active: true}
	must(s.CreatePlan(ctx, plan))
	cfg := &AIPlanRecord{Config: model.AIPlanSettings{GroupID: 53, Quota: .01}, ProfileBinding: "synthetic-profile"}
	must(s.SaveAIPlan(ctx, plan.ID, cfg))
	b := &InventoryBatch{ProductID: p.ID, PlanID: plan.ID, ActorID: "synthetic-admin", RequestKey: NewID(), Quantity: 2}
	must(s.CreateInventory(ctx, b, cfg))
	originalID := b.ID
	must(s.CreateInventory(ctx, b, cfg))
	if b.ID != originalID {
		t.Fatal("duplicate inventory")
	}
	b.Quantity = 3
	if !errors.Is(s.CreateInventory(ctx, b, cfg), ErrInventoryConflict) {
		t.Fatal("idempotency conflict not rejected")
	}
	// Parallel retries use the same durable batch, including across request timeouts.
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := &InventoryBatch{ProductID: p.ID, PlanID: plan.ID, ActorID: "synthetic-admin", RequestKey: b.RequestKey, Quantity: 2}
			if err := s.CreateInventory(ctx, copy, cfg); err != nil {
				failures <- err
			} else if copy.ID != originalID {
				failures <- errors.New("concurrent batch duplicate")
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		must(err)
	}
	var rows []*model.License
	must(s.DB.NewSelect().Model(&rows).Where("issuance_batch_id=?", b.ID).Order("id").Scan(ctx))
	if len(rows) != 2 || rows[0].Email != "" || rows[0].ValidUntil != nil || rows[0].Status != model.StatusPendingActivation {
		t.Fatal("stock started early")
	}
	jobs, err := s.AIJobs(ctx, rows[0].ID)
	must(err)
	if len(jobs) != 0 {
		t.Fatal("stock remote task created before activation")
	}
	cfg.Config.GroupID = 35
	must(s.SaveAIPlan(ctx, plan.ID, cfg))
	jobs, err = s.AIJobs(ctx, rows[0].ID)
	must(err)
	if len(jobs) != 0 {
		t.Fatal("plan save started stock")
	}
	// Invalid mixed selections do not mark the valid row as exported.
	invalid := &KeyExport{ActorID: "synthetic-admin", RequestKey: NewID(), LicenseIDs: []string{rows[0].ID, "missing"}}
	if !errors.Is(s.CreateKeyExport(ctx, invalid), ErrInventoryIneligible) {
		t.Fatal("mixed invalid selection accepted")
	}
	var exportMark string
	must(s.DB.NewRaw("SELECT first_export_id FROM licenses WHERE id=?", rows[0].ID).Scan(ctx, &exportMark))
	if exportMark != "" {
		t.Fatal("partial export marker")
	}
	e := &KeyExport{ActorID: "synthetic-admin", RequestKey: NewID(), LicenseIDs: []string{rows[0].ID, rows[1].ID}}
	must(s.CreateKeyExport(ctx, e))
	exportID := e.ID
	must(s.CreateKeyExport(ctx, e))
	if e.ID != exportID {
		t.Fatal("duplicate export manifest")
	}
	if !errors.Is(s.CreateKeyExport(ctx, &KeyExport{ActorID: "synthetic-admin", RequestKey: NewID(), LicenseIDs: e.LicenseIDs}), ErrInventoryIneligible) {
		t.Fatal("stock reexported as new")
	}
	if !errors.Is(s.DeletePlan(ctx, plan.ID), ErrPlanHasLicenses) {
		t.Fatal("plan deleted with stock")
	}
	act := &model.Activation{Identifier: "synthetic-device", IdentifierType: "device", DevicePublicKey: "synthetic-public"}
	started, err := s.StartInventory(ctx, rows[0].ID, act)
	must(err)
	if !started {
		t.Fatal("first activation not started")
	}
	l, err := s.FindLicenseByID(ctx, rows[0].ID)
	must(err)
	anchor := l.ValidFrom
	if l.ValidUntil == nil || l.ValidUntil.Before(anchor.Add(27*24*time.Hour)) {
		t.Fatal("subscription period missing")
	}
	started, err = s.StartInventory(ctx, l.ID, act)
	must(err)
	if started {
		t.Fatal("period started twice")
	}
	if _, err = s.StartInventory(ctx, l.ID, &model.Activation{Identifier: "different", DevicePublicKey: "other"}); !errors.Is(err, ErrInventoryConflict) {
		t.Fatal("second device accepted")
	}
	binding, err := s.AIBinding(ctx, l.ID)
	must(err)
	if !binding.Anchor.Equal(anchor) || binding.Config.GroupID != 35 {
		t.Fatal("anchor or latest stock configuration mismatch")
	}
	// Match production symptom: database COMMIT succeeds but the driver reports an error.
	locked, err := s.DB.BeginTx(ctx, nil)
	must(err)
	_, err = locked.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "ai-license:"+l.ID)
	must(err)
	began := time.Now()
	_, err = s.DeleteRevokedUnpaidLicense(ctx, l.ID, "synthetic-admin", "")
	if !errors.Is(err, ErrAIDeleteBusy) || time.Since(began) > time.Second {
		t.Fatal("deletion waited on AI lock")
	}
	must(locked.Rollback())
	_, err = s.DB.ExecContext(ctx, "UPDATE licenses SET status='revoked' WHERE id IN (?)", bun.In(e.LicenseIDs))
	must(err)
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET state='done' WHERE license_id IN (?)", bun.In(e.LicenseIDs))
	must(err)
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_bindings SET state='synced',remote_view='{\"status\":\"inactive\"}' WHERE license_id IN (?)", bun.In(e.LicenseIDs))
	must(err)
	failure := new(atomic.Bool)
	failure.Store(true)
	sqldb := sql.OpenDB(uncertainConnector{Connector: pgdriver.NewConnector(pgdriver.WithDSN(dsn)), fail: failure})
	originalDB := s.DB
	s.DB = bun.NewDB(sqldb, pgdialect.New())
	_, err = s.DeleteRevokedUnpaidLicense(ctx, l.ID, "synthetic-admin", "")
	must(err)
	s.DB.Close()
	s.DB = originalDB
	var count int
	must(s.DB.NewRaw("SELECT count(*) FROM audit_logs WHERE entity_id=? AND action='deleted'", l.ID).Scan(ctx, &count))
	if count != 1 {
		t.Fatal("confirmed delete audit missing")
	}
	_, err = s.DeleteRevokedUnpaidLicense(ctx, rows[1].ID, "synthetic-admin", "")
	must(err)
	must(s.DeletePlan(ctx, plan.ID))
	// Different admin sessions cannot allocate the same fresh inventory twice.
	plan2 := &model.Plan{ProductID: p.ID, Name: "Concurrent export", Slug: NewID(), LicenseType: "subscription", LicenseModel: "standard", BillingInterval: "year", MaxActivations: 1, GraceDays: 0, Active: true}
	must(s.CreatePlan(ctx, plan2))
	cfg2 := &AIPlanRecord{Config: cfg.Config, ProfileBinding: cfg.ProfileBinding}
	must(s.SaveAIPlan(ctx, plan2.ID, cfg2))
	b2 := &InventoryBatch{ProductID: p.ID, PlanID: plan2.ID, ActorID: "synthetic-admin", RequestKey: NewID(), Quantity: 1}
	must(s.CreateInventory(ctx, b2, cfg2))
	var extra model.License
	must(s.DB.NewSelect().Model(&extra).Where("issuance_batch_id=?", b2.ID).Scan(ctx))
	successes := atomic.Int32{}
	start := make(chan struct{})
	wg = sync.WaitGroup{}
	for _, actor := range []string{"synthetic-a", "synthetic-b"} {
		wg.Add(1)
		go func(actor string) {
			defer wg.Done()
			<-start
			if s.CreateKeyExport(ctx, &KeyExport{ActorID: actor, RequestKey: NewID(), LicenseIDs: []string{extra.ID}}) == nil {
				successes.Add(1)
			}
		}(actor)
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("concurrent export allocated twice")
	}
	must(s.RevokeLicense(ctx, extra.ID))
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET state='done' WHERE license_id=?", extra.ID)
	must(err)
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_bindings SET state='synced' WHERE license_id=?", extra.ID)
	must(err)
	_, err = s.DeleteRevokedUnpaidLicense(ctx, extra.ID, "synthetic-admin", "")
	must(err)
	must(s.DeletePlan(ctx, plan2.ID))
	var history KeyExport
	must(s.DB.NewSelect().Model(&history).Where("id=?", exportID).Scan(ctx))
}
