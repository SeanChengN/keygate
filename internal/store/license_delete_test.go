package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
)

func TestDeleteRevokedUnpaidLicense_CascadesAndPreservesAudit(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	ctx := context.Background()
	lic := createDeleteTestLicense(t, s, ctx)
	lic.Status = model.StatusRevoked
	if err := s.UpdateLicense(ctx, lic, "status"); err != nil {
		t.Fatalf("revoke test license: %v", err)
	}

	subscription := &model.Subscription{
		ID: store.NewID(), LicenseID: lic.ID, PlanID: lic.PlanID, Status: model.StatusRevoked,
	}
	activation := &model.Activation{
		ID: store.NewID(), LicenseID: lic.ID, Identifier: "delete-device", IdentifierType: "device",
	}
	seat := &model.Seat{
		ID: store.NewID(), LicenseID: lic.ID, Email: "seat-delete@example.com", Role: "member",
	}
	usage := &model.UsageEvent{
		ID: store.NewID(), LicenseID: lic.ID, Feature: "delete-test", Quantity: 1,
	}
	floating := &model.FloatingSession{
		ID: store.NewID(), LicenseID: lic.ID, Identifier: "delete-floating", ExpiresAt: time.Now().Add(time.Hour),
	}
	for name, row := range map[string]any{
		"subscription": subscription,
		"activation":   activation,
		"seat":         seat,
		"usage":        usage,
		"floating":     floating,
	} {
		if _, err := s.DB.NewInsert().Model(row).Exec(ctx); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}

	deleted, err := s.DeleteRevokedUnpaidLicense(ctx, lic.ID, "admin-delete-test", "127.0.0.1")
	if err != nil {
		t.Fatalf("delete revoked unpaid license: %v", err)
	}
	if deleted.ID != lic.ID || deleted.ProductID != lic.ProductID || deleted.PlanID != lic.PlanID || deleted.Email != lic.Email {
		t.Fatalf("unexpected deleted snapshot: %#v", deleted)
	}

	licenseCount, err := s.DB.NewSelect().Model((*model.License)(nil)).Where("id = ?", lic.ID).Count(ctx)
	if err != nil {
		t.Fatalf("count deleted license: %v", err)
	}
	if licenseCount != 0 {
		t.Fatalf("license count = %d, want 0", licenseCount)
	}
	for _, table := range []string{"subscriptions", "activations", "seats", "usage_events", "floating_sessions"} {
		count, err := s.DB.NewSelect().TableExpr(table).Where("license_id = ?", lic.ID).Count(ctx)
		if err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", table, count)
		}
	}

	audit := new(model.AuditLog)
	if err := s.DB.NewSelect().Model(audit).
		Where("entity = 'license' AND entity_id = ? AND action = 'deleted'", lic.ID).
		Scan(ctx); err != nil {
		t.Fatalf("find deletion audit: %v", err)
	}
	if audit.ActorID != "admin-delete-test" || audit.IPAddress != "127.0.0.1" {
		t.Fatalf("unexpected audit actor: %#v", audit)
	}
	if _, leaked := audit.Changes["license_key"]; leaked {
		t.Fatal("deletion audit must not contain license_key")
	}
	auditJSON, err := json.Marshal(audit.Changes)
	if err != nil {
		t.Fatalf("marshal audit changes: %v", err)
	}
	if strings.Contains(strings.ToLower(string(auditJSON)), strings.ToLower(lic.LicenseKey)) {
		t.Fatal("deletion audit leaked the license key")
	}

	logs, total, err := s.ListAuditLogs(ctx, "license", "", lic.ProductID, 0, 20)
	if err != nil {
		t.Fatalf("list product-filtered audits: %v", err)
	}
	if total == 0 || len(logs) == 0 {
		t.Fatal("deleted license audit must remain visible in the product-filtered audit log")
	}
}

func TestDeleteRevokedUnpaidLicense_RejectsUnsafeTargets(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	ctx := context.Background()

	t.Run("not revoked", func(t *testing.T) {
		lic := createDeleteTestLicense(t, s, ctx)
		if _, err := s.DeleteRevokedUnpaidLicense(ctx, lic.ID, "admin", ""); !errors.Is(err, store.ErrLicenseNotRevoked) {
			t.Fatalf("error = %v, want ErrLicenseNotRevoked", err)
		}
		assertLicenseExists(t, s, ctx, lic.ID)
	})

	t.Run("license payment fields", func(t *testing.T) {
		lic := createDeleteTestLicense(t, s, ctx)
		lic.Status = model.StatusRevoked
		lic.PaymentProvider = "stripe"
		lic.StripeSubscriptionID = "sub_delete_protected_" + lic.ID
		if err := s.UpdateLicense(ctx, lic, "status", "payment_provider", "stripe_subscription_id"); err != nil {
			t.Fatalf("prepare paid license: %v", err)
		}
		if _, err := s.DeleteRevokedUnpaidLicense(ctx, lic.ID, "admin", ""); !errors.Is(err, store.ErrLicensePaymentLinked) {
			t.Fatalf("error = %v, want ErrLicensePaymentLinked", err)
		}
		assertLicenseExists(t, s, ctx, lic.ID)
	})

	t.Run("subscription payment fields", func(t *testing.T) {
		lic := createDeleteTestLicense(t, s, ctx)
		lic.Status = model.StatusRevoked
		if err := s.UpdateLicense(ctx, lic, "status"); err != nil {
			t.Fatalf("revoke test license: %v", err)
		}
		subscription := &model.Subscription{
			ID: store.NewID(), LicenseID: lic.ID, PlanID: lic.PlanID, Status: model.StatusRevoked,
			ExternalID: "sub_external_delete_protected",
		}
		if _, err := s.DB.NewInsert().Model(subscription).Exec(ctx); err != nil {
			t.Fatalf("insert paid subscription: %v", err)
		}
		if _, err := s.DeleteRevokedUnpaidLicense(ctx, lic.ID, "admin", ""); !errors.Is(err, store.ErrLicensePaymentLinked) {
			t.Fatalf("error = %v, want ErrLicensePaymentLinked", err)
		}
		assertLicenseExists(t, s, ctx, lic.ID)
		count, err := s.DB.NewSelect().Model((*model.Subscription)(nil)).Where("license_id = ?", lic.ID).Count(ctx)
		if err != nil || count != 1 {
			t.Fatalf("protected subscription count = %d, err = %v", count, err)
		}
	})

	t.Run("missing", func(t *testing.T) {
		if _, err := s.DeleteRevokedUnpaidLicense(ctx, "missing-license", "admin", ""); !errors.Is(err, store.ErrLicenseNotFound) {
			t.Fatalf("error = %v, want ErrLicenseNotFound", err)
		}
	})
}

func assertLicenseExists(t *testing.T, s *store.Store, ctx context.Context, id string) {
	t.Helper()
	count, err := s.DB.NewSelect().Model((*model.License)(nil)).Where("id = ?", id).Count(ctx)
	if err != nil {
		t.Fatalf("count license: %v", err)
	}
	if count != 1 {
		t.Fatalf("license count = %d, want 1", count)
	}
}

func createDeleteTestLicense(t *testing.T, s *store.Store, ctx context.Context) *model.License {
	t.Helper()
	suffix := store.NewID()
	product := &model.Product{Name: "License Delete Test", Slug: "license-delete-" + suffix, Type: "hybrid"}
	if err := s.CreateProduct(ctx, product); err != nil {
		t.Fatalf("create delete-test product: %v", err)
	}
	plan := &model.Plan{
		ProductID: product.ID, Name: "Delete Test", Slug: "delete-test-" + suffix,
		LicenseType: "perpetual", LicenseModel: "standard",
	}
	if err := s.CreatePlan(ctx, plan); err != nil {
		t.Fatalf("create delete-test plan: %v", err)
	}
	lic := &model.License{
		ProductID: product.ID, PlanID: plan.ID,
		Email:      "license-delete-" + suffix + "@example.com",
		LicenseKey: "SECRET-" + suffix, Status: model.StatusActive,
	}
	if err := s.CreateLicense(ctx, lic); err != nil {
		t.Fatalf("create delete-test license: %v", err)
	}
	return lic
}
