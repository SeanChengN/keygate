package store_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
)

func setupTestDB(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("skipping integration test: TEST_DATABASE_URL not set")
	}
	s, err := store.New(dsn)
	if err != nil {
		t.Skipf("skipping integration test: %v", err)
	}
	if err := s.RunMigrations("../../db/migrations"); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
	return s
}

func createTestLicense(t *testing.T, s *store.Store, ctx context.Context) *model.License {
	t.Helper()
	suffix := time.Now().Format("150405.000")
	product := &model.Product{Name: "Usage Test", Slug: "usage-test-" + suffix, Type: "saas"}
	if err := s.CreateProduct(ctx, product); err != nil {
		t.Fatalf("create product: %v", err)
	}
	plan := &model.Plan{
		ProductID:       product.ID,
		Name:            "Test Plan",
		Slug:            "test-plan-" + suffix,
		LicenseType:     "subscription",
		BillingInterval: "month",
		LicenseModel:    "standard",
		GraceDays:       7,
	}
	if err := s.CreatePlan(ctx, plan); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	lic := &model.License{
		ProductID:  product.ID,
		PlanID:     plan.ID,
		Email:      "test-" + suffix + "@example.com",
		LicenseKey: "KEY-" + suffix,
		Status:     "active",
	}
	if err := s.CreateLicense(ctx, lic); err != nil {
		t.Fatalf("create license: %v", err)
	}
	return lic
}

func TestIncrementUsageCounterWithLimit_Atomic(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	ctx := context.Background()

	lic := createTestLicense(t, s, ctx)
	licenseID := lic.ID
	feature := "api_calls"
	period := "monthly"
	periodKey := "2026-03"
	limit := int64(100)

	_, _ = s.DB.NewRaw("DELETE FROM usage_counters WHERE license_id = ?", licenseID).Exec(ctx)

	counter, accepted, err := s.IncrementUsageCounterWithLimit(ctx, licenseID, feature, period, periodKey, 10, limit)
	if err != nil {
		t.Fatalf("increment failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected increment to be accepted")
	}
	if counter.Used != 10 {
		t.Fatalf("expected used=10, got %d", counter.Used)
	}

	counter, accepted, err = s.IncrementUsageCounterWithLimit(ctx, licenseID, feature, period, periodKey, 90, limit)
	if err != nil {
		t.Fatalf("increment failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected increment to be accepted (exactly at limit)")
	}
	if counter.Used != 100 {
		t.Fatalf("expected used=100, got %d", counter.Used)
	}

	counter, accepted, err = s.IncrementUsageCounterWithLimit(ctx, licenseID, feature, period, periodKey, 1, limit)
	if err != nil {
		t.Fatalf("increment failed: %v", err)
	}
	if accepted {
		t.Fatal("expected increment to be rejected (would exceed limit)")
	}
	if counter.Used != 100 {
		t.Fatalf("expected used=100 (unchanged), got %d", counter.Used)
	}

	_, _ = s.DB.NewRaw("DELETE FROM usage_counters WHERE license_id = ?", licenseID).Exec(ctx)

	concurrency := 20
	quantity := int64(10)
	limit = 100 // Only 10 of 20 goroutines should succeed

	var wg sync.WaitGroup
	acceptedCount := int64(0)
	var mu sync.Mutex

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := s.IncrementUsageCounterWithLimit(ctx, licenseID, feature, period, periodKey, quantity, limit)
			if err != nil {
				t.Errorf("concurrent increment failed: %v", err)
				return
			}
			if ok {
				mu.Lock()
				acceptedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	finalCounter, err := s.GetUsageCounter(ctx, licenseID, feature, period, periodKey)
	if err != nil {
		t.Fatalf("get counter failed: %v", err)
	}
	if finalCounter.Used > limit {
		t.Fatalf("RACE CONDITION: counter exceeded limit! used=%d, limit=%d", finalCounter.Used, limit)
	}
	if acceptedCount != limit/quantity {
		t.Logf("accepted %d of %d attempts (expected %d)", acceptedCount, concurrency, limit/quantity)
	}
	t.Logf("concurrent test passed: used=%d, limit=%d, accepted=%d/%d", finalCounter.Used, limit, acceptedCount, concurrency)

	_, _ = s.DB.NewRaw("DELETE FROM usage_counters WHERE license_id = ?", licenseID).Exec(ctx)
	counter, accepted, err = s.IncrementUsageCounterWithLimit(ctx, licenseID, feature, period, periodKey, 99999, 0)
	if err != nil {
		t.Fatalf("unlimited increment failed: %v", err)
	}
	if !accepted {
		t.Fatal("expected unlimited increment to be accepted")
	}
	if counter.Used != 99999 {
		t.Fatalf("expected used=99999, got %d", counter.Used)
	}

	_, _ = s.DB.NewRaw("DELETE FROM usage_counters WHERE license_id = ?", licenseID).Exec(ctx)
}

func TestCreateLicenseWithSubscription_Atomic(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	ctx := context.Background()

	product := &model.Product{Name: "Test Product", Slug: "test-tx-" + time.Now().Format("150405.000000"), Type: "saas"}
	if err := s.CreateProduct(ctx, product); err != nil {
		t.Fatalf("create product: %v", err)
	}

	plan := &model.Plan{
		ProductID:       product.ID,
		Name:            "Pro",
		Slug:            "pro",
		LicenseType:     "subscription",
		BillingInterval: "month",
		LicenseModel:    "standard",
		GraceDays:       7,
	}
	if err := s.CreatePlan(ctx, plan); err != nil {
		t.Fatalf("create plan: %v", err)
	}

	lic := &model.License{
		ProductID:  product.ID,
		PlanID:     plan.ID,
		Email:      "test@example.com",
		LicenseKey: "TEST-" + time.Now().Format("150405.000000"),
		Status:     "active",
	}
	if err := s.CreateLicenseWithSubscription(ctx, lic, plan); err != nil {
		t.Fatalf("create license with subscription: %v", err)
	}

	// Verify license exists
	found, err := s.FindLicenseByID(ctx, lic.ID)
	if err != nil {
		t.Fatalf("find license: %v", err)
	}
	if found.Email != "test@example.com" {
		t.Fatalf("unexpected email: %s", found.Email)
	}

	sub, err := s.FindSubscriptionByLicense(ctx, lic.ID)
	if err != nil {
		t.Fatalf("find subscription: %v", err)
	}
	if sub.PlanID != plan.ID {
		t.Fatalf("unexpected plan_id: %s", sub.PlanID)
	}
	if sub.Status != "active" {
		t.Fatalf("unexpected subscription status: %s", sub.Status)
	}

	trialPlan := &model.Plan{
		ProductID:    product.ID,
		Name:         "Trial",
		Slug:         "trial",
		LicenseType:  "trial",
		LicenseModel: "standard",
		TrialDays:    14,
		GraceDays:    0,
	}
	if err := s.CreatePlan(ctx, trialPlan); err != nil {
		t.Fatalf("create trial plan: %v", err)
	}

	trialUntil := time.Now().Add(14 * 24 * time.Hour)
	trialLic := &model.License{
		ProductID:  product.ID,
		PlanID:     trialPlan.ID,
		Email:      "trial@example.com",
		LicenseKey: "TRIAL-" + time.Now().Format("150405.000000"),
		Status:     "trialing",
		ValidUntil: &trialUntil,
	}
	if err := s.CreateLicenseWithSubscription(ctx, trialLic, trialPlan); err != nil {
		t.Fatalf("create trial license: %v", err)
	}

	trialSub, err := s.FindSubscriptionByLicense(ctx, trialLic.ID)
	if err != nil {
		t.Fatalf("find trial subscription: %v", err)
	}
	if trialSub.TrialStart == nil {
		t.Fatal("expected trial_start to be set")
	}
	if trialSub.TrialEnd == nil {
		t.Fatal("expected trial_end to be set")
	}
	if trialSub.Status != "trialing" {
		t.Fatalf("expected status=trialing, got %s", trialSub.Status)
	}

	t.Log("transaction test passed: license and subscription created atomically")
}

func TestManualSubscriptionRenewalAndPlanChange(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")

	product := &model.Product{Name: "Renewal Test", Slug: "renewal-" + suffix, Type: "desktop"}
	if err := s.CreateProduct(ctx, product); err != nil {
		t.Fatalf("create product: %v", err)
	}
	monthly := &model.Plan{
		ProductID: product.ID, Name: "Monthly", Slug: "monthly",
		LicenseType: "subscription", BillingInterval: "month", LicenseModel: "standard", GraceDays: 7,
	}
	yearly := &model.Plan{
		ProductID: product.ID, Name: "Yearly", Slug: "yearly",
		LicenseType: "subscription", BillingInterval: "year", LicenseModel: "standard", GraceDays: 7,
	}
	if err := s.CreatePlan(ctx, monthly); err != nil {
		t.Fatalf("create monthly plan: %v", err)
	}
	if err := s.CreatePlan(ctx, yearly); err != nil {
		t.Fatalf("create yearly plan: %v", err)
	}
	webhook := &model.Webhook{
		ProductID: product.ID, URL: "https://example.com/webhook", Secret: "test-secret",
		Events: []string{"license.renewed"}, Active: true,
	}
	if err := s.CreateWebhook(ctx, webhook); err != nil {
		t.Fatalf("create renewal webhook: %v", err)
	}

	validFrom := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	validUntil := time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC)
	lic := &model.License{
		ProductID: product.ID, PlanID: monthly.ID, Email: "early@example.com",
		LicenseKey: "EARLY-" + suffix, Status: model.StatusActive,
		ValidFrom: validFrom, ValidUntil: &validUntil,
	}
	if err := s.CreateLicenseWithSubscription(ctx, lic, monthly); err != nil {
		t.Fatalf("create early renewal license: %v", err)
	}

	renewed, err := s.RenewManualSubscription(ctx, lic.ID, "admin-test", time.Date(2026, time.January, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("early renewal: %v", err)
	}
	wantMonthlyEnd := time.Date(2026, time.February, 28, 0, 0, 0, 0, time.UTC)
	if !renewed.CurrentPeriodEnd.Equal(wantMonthlyEnd) {
		t.Fatalf("early renewal end = %s, want %s", renewed.CurrentPeriodEnd, wantMonthlyEnd)
	}
	if !renewed.CurrentPeriodStart.Equal(validFrom) {
		t.Fatalf("early renewal changed period start: %s", renewed.CurrentPeriodStart)
	}

	if err := s.ChangeLicensePlan(ctx, lic.ID, yearly.ID); err != nil {
		t.Fatalf("change plan: %v", err)
	}
	changed, err := s.FindLicenseByID(ctx, lic.ID)
	if err != nil {
		t.Fatalf("find changed license: %v", err)
	}
	if changed.PlanID != yearly.ID || changed.ValidUntil == nil || !changed.ValidUntil.Equal(wantMonthlyEnd) {
		t.Fatalf("plan change did not preserve paid-through date: plan=%s until=%v", changed.PlanID, changed.ValidUntil)
	}
	sub, err := s.FindSubscriptionByLicense(ctx, lic.ID)
	if err != nil {
		t.Fatalf("find changed subscription: %v", err)
	}
	if sub.PlanID != yearly.ID {
		t.Fatalf("subscription plan = %s, want %s", sub.PlanID, yearly.ID)
	}

	renewed, err = s.RenewManualSubscription(ctx, lic.ID, "admin-test", time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("yearly renewal after plan change: %v", err)
	}
	wantYearlyEnd := time.Date(2027, time.February, 28, 0, 0, 0, 0, time.UTC)
	if !renewed.CurrentPeriodEnd.Equal(wantYearlyEnd) {
		t.Fatalf("yearly renewal end = %s, want %s", renewed.CurrentPeriodEnd, wantYearlyEnd)
	}

	lateUntil := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	late := &model.License{
		ProductID: product.ID, PlanID: monthly.ID, Email: "late@example.com",
		LicenseKey: "LATE-" + suffix, Status: model.StatusExpired,
		ValidFrom: validFrom, ValidUntil: &lateUntil,
	}
	if err := s.CreateLicenseWithSubscription(ctx, late, monthly); err != nil {
		t.Fatalf("create late renewal license: %v", err)
	}
	lateResult, err := s.RenewManualSubscription(ctx, late.ID, "admin-test", time.Date(2024, time.January, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("late renewal: %v", err)
	}
	wantLeapEnd := time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC)
	if !lateResult.CurrentPeriodEnd.Equal(wantLeapEnd) || lateResult.License.Status != model.StatusActive {
		t.Fatalf("late renewal = (%s, %s), want (%s, active)", lateResult.CurrentPeriodEnd, lateResult.License.Status, wantLeapEnd)
	}

	suspended := &model.License{
		ProductID: product.ID, PlanID: monthly.ID, Email: "blocked@example.com",
		LicenseKey: "BLOCKED-" + suffix, Status: model.StatusSuspended,
		ValidFrom: validFrom, ValidUntil: &validUntil,
	}
	if err := s.CreateLicenseWithSubscription(ctx, suspended, monthly); err != nil {
		t.Fatalf("create suspended license: %v", err)
	}
	if _, err := s.RenewManualSubscription(ctx, suspended.ID, "admin-test", time.Now()); !errors.Is(err, store.ErrManualRenewalBlocked) {
		t.Fatalf("suspended renewal error = %v, want ErrManualRenewalBlocked", err)
	}

	stripe := &model.License{
		ProductID: product.ID, PlanID: monthly.ID, Email: "stripe@example.com",
		LicenseKey: "STRIPE-" + suffix, Status: model.StatusActive,
		PaymentProvider: "stripe", StripeSubscriptionID: "sub_" + suffix,
		ValidFrom: validFrom, ValidUntil: &validUntil,
	}
	if err := s.CreateLicenseWithSubscription(ctx, stripe, monthly); err != nil {
		t.Fatalf("create Stripe license: %v", err)
	}
	if _, err := s.RenewManualSubscription(ctx, stripe.ID, "admin-test", time.Now()); !errors.Is(err, store.ErrManualRenewalNotSubscription) {
		t.Fatalf("Stripe renewal error = %v, want ErrManualRenewalNotSubscription", err)
	}

	audits, err := s.DB.NewSelect().Model((*model.AuditLog)(nil)).
		Where("entity_id = ? AND action = 'renewed'", lic.ID).Count(ctx)
	if err != nil {
		t.Fatalf("count renewal audits: %v", err)
	}
	if audits != 2 {
		t.Fatalf("renewal audit count = %d, want 2", audits)
	}
	deliveries, err := s.DB.NewSelect().Model((*model.WebhookDelivery)(nil)).
		Where("webhook_id = ? AND event = 'license.renewed'", webhook.ID).Count(ctx)
	if err != nil {
		t.Fatalf("count renewal webhook deliveries: %v", err)
	}
	if deliveries != 3 {
		t.Fatalf("renewal webhook delivery count = %d, want 3", deliveries)
	}
}

func TestSubscriptionPeriodMigrationBackfillsLegacyRows(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin migration test transaction: %v", err)
	}
	defer tx.Rollback()

	if _, err := tx.NewRaw("ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_license_type_billing_interval_check").Exec(ctx); err != nil {
		t.Fatalf("drop current billing constraint: %v", err)
	}
	suffix := time.Now().Format("150405.000000")
	product := &model.Product{ID: "product-legacy-" + suffix, Name: "Legacy Product", Slug: "legacy-product-" + suffix, Type: "desktop"}
	if _, err := tx.NewInsert().Model(product).Exec(ctx); err != nil {
		t.Fatalf("insert legacy product: %v", err)
	}
	plan := &model.Plan{
		ID: "plan-legacy-" + suffix, ProductID: product.ID, Name: "Legacy Subscription", Slug: "legacy",
		CheckoutID:  "lg" + strings.ReplaceAll(suffix, ".", ""),
		LicenseType: "subscription", BillingInterval: "", LicenseModel: "standard", GraceDays: 7,
	}
	if _, err := tx.NewInsert().Model(plan).Exec(ctx); err != nil {
		t.Fatalf("insert legacy plan: %v", err)
	}
	validFrom := time.Date(2024, time.January, 31, 12, 0, 0, 0, time.UTC)
	lic := &model.License{
		ID: "license-legacy-" + suffix, ProductID: product.ID, PlanID: plan.ID,
		Email: "legacy@example.com", LicenseKey: "LEGACY-" + suffix,
		Status: model.StatusActive, ValidFrom: validFrom,
	}
	if _, err := tx.NewInsert().Model(lic).Exec(ctx); err != nil {
		t.Fatalf("insert legacy license: %v", err)
	}
	sub := &model.Subscription{
		ID: "subscription-legacy-" + suffix, LicenseID: lic.ID, PlanID: plan.ID, Status: model.StatusActive,
	}
	if _, err := tx.NewInsert().Model(sub).Exec(ctx); err != nil {
		t.Fatalf("insert legacy subscription: %v", err)
	}

	migrationSQL, err := os.ReadFile("../../db/migrations/20260811_subscription_periods.up.sql")
	if err != nil {
		t.Fatalf("read subscription migration: %v", err)
	}
	if _, err := tx.NewRaw(string(migrationSQL)).Exec(ctx); err != nil {
		t.Fatalf("execute subscription migration: %v", err)
	}
	if err := tx.NewSelect().Model(plan).WherePK().Scan(ctx); err != nil {
		t.Fatalf("reload migrated plan: %v", err)
	}
	if plan.BillingInterval != "month" {
		t.Fatalf("legacy billing interval = %q, want month", plan.BillingInterval)
	}
	if err := tx.NewSelect().Model(lic).WherePK().Scan(ctx); err != nil {
		t.Fatalf("reload migrated license: %v", err)
	}
	wantEnd := time.Date(2024, time.February, 29, 12, 0, 0, 0, time.UTC)
	if lic.ValidUntil == nil || !lic.ValidUntil.Equal(wantEnd) || lic.Status != model.StatusExpired {
		t.Fatalf("migrated license = until %v status %s, want %s expired", lic.ValidUntil, lic.Status, wantEnd)
	}
	if err := tx.NewSelect().Model(sub).WherePK().Scan(ctx); err != nil {
		t.Fatalf("reload migrated subscription: %v", err)
	}
	if sub.CurrentPeriodStart == nil || !sub.CurrentPeriodStart.Equal(validFrom) ||
		sub.CurrentPeriodEnd == nil || !sub.CurrentPeriodEnd.Equal(wantEnd) || sub.Status != model.StatusExpired {
		t.Fatalf("migrated subscription period/status mismatch: %#v", sub)
	}
}

func TestFloatingCheckOutWithLimit_Atomic(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	ctx := context.Background()

	product := &model.Product{Name: "Float Test", Slug: "float-" + time.Now().Format("150405.000000"), Type: "desktop"}
	if err := s.CreateProduct(ctx, product); err != nil {
		t.Fatalf("create product: %v", err)
	}

	plan := &model.Plan{
		ProductID:       product.ID,
		Name:            "Float Plan",
		Slug:            "float",
		LicenseType:     "subscription",
		BillingInterval: "month",
		MaxActivations:  3,
		LicenseModel:    "floating",
		FloatingTimeout: 30,
	}
	if err := s.CreatePlan(ctx, plan); err != nil {
		t.Fatalf("create plan: %v", err)
	}

	lic := &model.License{
		ProductID:  product.ID,
		PlanID:     plan.ID,
		Email:      "float@test.com",
		LicenseKey: "FLOAT-" + time.Now().Format("150405.000000"),
		Status:     "active",
	}
	if err := s.CreateLicense(ctx, lic); err != nil {
		t.Fatalf("create license: %v", err)
	}

	maxSessions := 3

	sess1 := &model.FloatingSession{
		LicenseID:  lic.ID,
		Identifier: "device-1",
		ExpiresAt:  time.Now().Add(30 * time.Minute),
	}
	isNew, err := s.CheckOutFloatingWithLimit(ctx, sess1, maxSessions)
	if err != nil {
		t.Fatalf("checkout 1 failed: %v", err)
	}
	if !isNew {
		t.Fatal("expected new session")
	}

	sess1dup := &model.FloatingSession{
		LicenseID:  lic.ID,
		Identifier: "device-1",
		ExpiresAt:  time.Now().Add(60 * time.Minute),
	}
	isNew, err = s.CheckOutFloatingWithLimit(ctx, sess1dup, maxSessions)
	if err != nil {
		t.Fatalf("checkout 1 refresh failed: %v", err)
	}
	if isNew {
		t.Fatal("expected session refresh, not new")
	}

	for i := 2; i <= maxSessions; i++ {
		sess := &model.FloatingSession{
			LicenseID:  lic.ID,
			Identifier: "device-" + time.Now().Format("150405.000000") + "-" + string(rune('0'+i)),
			ExpiresAt:  time.Now().Add(30 * time.Minute),
		}
		isNew, err := s.CheckOutFloatingWithLimit(ctx, sess, maxSessions)
		if err != nil {
			t.Fatalf("checkout %d failed: %v", i, err)
		}
		if !isNew {
			t.Fatalf("expected new session for device-%d", i)
		}
	}

	sessOver := &model.FloatingSession{
		LicenseID:  lic.ID,
		Identifier: "device-over",
		ExpiresAt:  time.Now().Add(30 * time.Minute),
	}
	_, err = s.CheckOutFloatingWithLimit(ctx, sessOver, maxSessions)
	if err == nil {
		t.Fatal("expected error when exceeding limit")
	}
	if err.Error() != "floating session limit reached" {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _ = s.DB.NewRaw("DELETE FROM floating_sessions WHERE license_id = ?", lic.ID).Exec(ctx)

	concurrency := 10
	maxSessions = 3

	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sess := &model.FloatingSession{
				LicenseID:  lic.ID,
				Identifier: time.Now().Format("150405.000000") + "-concurrent-" + string(rune('A'+idx)),
				ExpiresAt:  time.Now().Add(30 * time.Minute),
			}
			isNew, err := s.CheckOutFloatingWithLimit(ctx, sess, maxSessions)
			if err == nil && isNew {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	activeCount, _ := s.CountActiveFloating(ctx, lic.ID)
	if activeCount > maxSessions {
		t.Fatalf("RACE CONDITION: active sessions %d exceed max %d", activeCount, maxSessions)
	}
	if successCount > maxSessions {
		t.Fatalf("RACE CONDITION: %d sessions created, max is %d", successCount, maxSessions)
	}
	t.Logf("concurrent floating test passed: active=%d, max=%d, success=%d/%d", activeCount, maxSessions, successCount, concurrency)

	_, _ = s.DB.NewRaw("DELETE FROM floating_sessions WHERE license_id = ?", lic.ID).Exec(ctx)
}

func TestUpdateLicenseAndSubscription_Atomic(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	ctx := context.Background()

	product := &model.Product{Name: "Sync Test", Slug: "sync-" + time.Now().Format("150405.000000"), Type: "saas"}
	_ = s.CreateProduct(ctx, product)

	plan := &model.Plan{ProductID: product.ID, Name: "Basic", Slug: "basic", LicenseType: "subscription", BillingInterval: "month", LicenseModel: "standard"}
	_ = s.CreatePlan(ctx, plan)

	lic := &model.License{
		ProductID: product.ID, PlanID: plan.ID, Email: "sync@test.com",
		LicenseKey: "SYNC-" + time.Now().Format("150405.000000"), Status: "active",
	}
	_ = s.CreateLicenseWithSubscription(ctx, lic, plan)

	sub, err := s.FindSubscriptionByLicense(ctx, lic.ID)
	if err != nil {
		t.Fatalf("find subscription: %v", err)
	}
	if sub.Status != "active" {
		t.Fatalf("expected active, got %s", sub.Status)
	}

	lic.Status = "suspended"
	now := time.Now()
	lic.SuspendedAt = &now
	if err := s.UpdateLicenseAndSubscription(ctx, lic, "status", "suspended_at"); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	sub, err = s.FindSubscriptionByLicense(ctx, lic.ID)
	if err != nil {
		t.Fatalf("find subscription after update: %v", err)
	}
	if sub.Status != "suspended" {
		t.Fatalf("subscription status not synced: expected suspended, got %s", sub.Status)
	}

	t.Log("license-subscription sync test passed")
}
