package service

import (
	"context"
	"errors"
	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestAIManagementPropagationProvisionAndDelete(t *testing.T) {
	dsn := os.Getenv("AI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated AI_TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	s, err := store.New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(s.RunMigrations("../../db/migrations"))
	p := &model.Product{Name: "AI management", Slug: "movaura-studio", Type: "desktop"}
	if s.DB.NewSelect().Model(p).Where("slug='movaura-studio'").Scan(ctx) != nil {
		check(s.CreateProduct(ctx, p))
	}
	plan := &model.Plan{ProductID: p.ID, Name: "Manage", Slug: store.NewID(), LicenseType: "subscription", BillingInterval: "month", LicenseModel: "standard", GraceDays: 0}
	check(s.CreatePlan(ctx, plan))
	check(s.CreateEntitlement(ctx, &model.Entitlement{PlanID: plan.ID, Feature: "studio.ai_profile", ValueType: "string", Value: `{"provider_id":"jiji","base_url":"https://api-slb.jiji.cc/v1","protocol":"openai"}`}))
	plan, err = s.FindPlanByID(ctx, plan.ID)
	check(err)
	profile, err := AIProfileBinding(plan)
	check(err)
	end := time.Now().AddDate(0, 2, 0).Truncate(time.Second)
	makeLicense := func() *model.License {
		l := &model.License{ProductID: p.ID, PlanID: plan.ID, Email: "synthetic-management@example.invalid", LicenseKey: store.NewID(), Status: model.StatusActive, ValidFrom: time.Now().AddDate(0, -2, 0), ValidUntil: &end}
		check(s.CreateLicense(ctx, l))
		return l
	}
	old := makeLicense()
	other := makeLicense()
	cfg := model.AIPlanSettings{Enabled: false, GroupID: 53, Quota: .01, Rate5H: .005, Rate1D: .008, Rate7D: .01}
	record := &store.AIPlanRecord{Config: cfg, ProfileBinding: profile}
	check(s.SaveAIPlan(ctx, plan.ID, record))
	f := &fakeAI{keys: map[int64]*JijiKey{}}
	server := httptest.NewServer(f)
	defer server.Close()
	aead, err := crypto.NewAESGCM(make([]byte, 32))
	check(err)
	ai := &AIService{Store: s, AEAD: aead, Client: &JijiClient{BaseURL: server.URL, HTTP: server.Client()}, Email: &EmailService{enabled: true}}
	check(ai.SaveAccount(ctx, p.ID, true, &AIAccountTokens{"synthetic", "synthetic-refresh", time.Now().Add(time.Hour).Unix()}))
	preview, err := ai.PreviewProvision(ctx, plan.ID)
	check(err)
	if len(preview.Items) != 2 {
		t.Fatal("preview omitted legacy licenses")
	}
	batch, err := ai.Provision(ctx, plan.ID, "test-admin", "manual-1", record.Revision, []string{old.ID})
	check(err)
	repeated, err := ai.Provision(ctx, plan.ID, "test-admin", "manual-1", record.Revision, []string{old.ID})
	check(err)
	if repeated.ID != batch.ID {
		t.Fatal("idempotency duplicated batch")
	}
	ai.Tick(ctx)
	b, err := s.AIBinding(ctx, old.ID)
	check(err)
	if b.Origin != "manual" || b.State != "synced" || f.creates != 1 || f.resets != 0 {
		t.Fatalf("unexpected first grant: state=%s creates=%d resets=%d", b.State, f.creates, f.resets)
	}
	var mails int
	check(s.DB.NewRaw("SELECT count(*) FROM email_queue WHERE id=?", "ai-license-issued:"+old.ID).Scan(ctx, &mails))
	if mails != 0 {
		t.Fatal("manual enrollment sent duplicate license email")
	}
	keyID := b.RemoteID
	secret := string(b.Secret)
	f.keys[keyID].QuotaUsed = .006
	f.keys[keyID].Usage5H = .002
	cfg.GroupID = 35
	cfg.Quota = .02
	record = &store.AIPlanRecord{Config: cfg, ProfileBinding: profile}
	check(s.SaveAIPlan(ctx, plan.ID, record))
	if record.Affected != 1 {
		t.Fatal("affected count mismatch")
	}
	if _, err = ai.Provision(ctx, plan.ID, "test-admin", "stale", preview.Revision, []string{other.ID}); !errors.Is(err, ErrAIConfigChanged) {
		t.Fatal("stale preview accepted")
	}
	ai.Tick(ctx)
	b, err = s.AIBinding(ctx, old.ID)
	check(err)
	if b.RemoteID != keyID || string(b.Secret) != secret || f.keys[keyID].QuotaUsed != .006 || f.keys[keyID].Usage5H != .002 || f.keys[keyID].GroupID != 35 || f.resets != 0 {
		t.Fatal("group sync changed key or reset usage")
	}
	// Switching an existing key to unlimited is a configuration update only.
	cfg.Quota = 0
	record = &store.AIPlanRecord{Config: cfg, ProfileBinding: profile}
	check(s.SaveAIPlan(ctx, plan.ID, record))
	ai.Tick(ctx)
	b, err = s.AIBinding(ctx, old.ID)
	check(err)
	if b.Config.Quota != 0 || f.keys[keyID].Quota != 0 || b.RemoteID != keyID || string(b.Secret) != secret || f.keys[keyID].QuotaUsed != .006 || f.keys[keyID].Usage5H != .002 || f.resets != 0 || !f.keys[keyID].ExpiresAt.Equal(end) {
		t.Fatal("unlimited sync changed key, consumption, expiry or reset usage")
	}
	check(s.SuspendLicense(ctx, old.ID))
	ai.Tick(ctx)
	cfg.GroupID = 36
	record = &store.AIPlanRecord{Config: cfg, ProfileBinding: profile}
	check(s.SaveAIPlan(ctx, plan.ID, record))
	ai.Tick(ctx)
	if f.keys[keyID].Status != "inactive" {
		t.Fatal("plan sync reenabled suspended key")
	}
	check(s.RevokeLicense(ctx, old.ID))
	if _, err = s.DeleteRevokedUnpaidLicense(ctx, old.ID, "test-admin", ""); !errors.Is(err, store.ErrAIDeleteUnconfirmed) {
		t.Fatal("deleted before disable sync confirmation")
	}
	_, err = ai.RequestDeletion(ctx, old.ID, "test-admin", "")
	check(err)
	ai.Tick(ctx)
	if _, err = s.FindLicenseByID(ctx, old.ID); err == nil {
		t.Fatal("deletion did not finish")
	}
	if _, exists := f.keys[keyID]; exists {
		t.Fatal("remote key survived license deletion")
	}
	if _, err = s.AIBinding(ctx, old.ID); err == nil {
		t.Fatal("AI binding survived deletion")
	}
	progress, err := ai.ProvisionBatch(ctx, plan.ID, batch.ID)
	check(err)
	if progress.Results[0].State != "removed" {
		t.Fatal("deleted batch item missing tombstone state")
	}
	var audit int
	check(s.DB.NewRaw("SELECT count(*) FROM audit_logs WHERE entity_id=? AND action='deleted'", old.ID).Scan(ctx, &audit))
	if audit != 1 {
		t.Fatal("deletion audit missing")
	}
	if _, err = s.AIBinding(ctx, other.ID); err == nil {
		t.Fatal("plan save auto-enrolled unrelated legacy license")
	}
	// A batch is atomic across selected licenses and preserves skipped reasons.
	third := makeLicense()
	check(s.SuspendLicense(ctx, third.ID))
	batch2, err := ai.Provision(ctx, plan.ID, "test-admin", "multi", record.Revision, []string{other.ID, third.ID})
	check(err)
	if len(batch2.Results) != 2 {
		t.Fatal("batch items lost")
	}
	ai.Tick(ctx)
	progress, err = ai.ProvisionBatch(ctx, plan.ID, batch2.ID)
	check(err)
	for _, item := range progress.Results {
		if item.ID == other.ID && item.State != "synced" {
			t.Fatal("eligible batch item not synced")
		}
		if item.ID == third.ID && (item.State != "skipped" || item.Reason != "license_unavailable") {
			t.Fatal("unavailable item enrolled")
		}
	}
	// Sequential revisions collapse to the latest target without duplicate creates.
	beforeCreates := f.creates
	cfg.GroupID = 35
	record = &store.AIPlanRecord{Config: cfg, ProfileBinding: profile}
	check(s.SaveAIPlan(ctx, plan.ID, record))
	cfg.GroupID = 36
	record = &store.AIPlanRecord{Config: cfg, ProfileBinding: profile}
	check(s.SaveAIPlan(ctx, plan.ID, record))
	ai.Tick(ctx)
	ob, err := s.AIBinding(ctx, other.ID)
	check(err)
	if ob.Config.GroupID != 36 || f.keys[ob.RemoteID].GroupID != 36 || f.creates != beforeCreates {
		t.Fatal("old revision overwrote newest target")
	}
	check(s.RevokeLicense(ctx, other.ID))
	ai.Tick(ctx)
	check(store.EnqueueAI(ctx, s.DB, other.ID, "reset_quota", "uncertain-delete"))
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET state='uncertain', phase='reset_sent' WHERE license_id=? AND dedupe_key='uncertain-delete'", other.ID)
	check(err)
	if _, err = s.DeleteRevokedUnpaidLicense(ctx, other.ID, "test-admin", ""); !errors.Is(err, store.ErrAIDeleteUnconfirmed) {
		t.Fatal("uncertain reset did not prevent deletion")
	}
	rollbackLicense := makeLicense()
	if _, err = ai.Provision(ctx, plan.ID, "test-admin", "rollback", record.Revision, []string{rollbackLicense.ID, "zz-invalid-license"}); err == nil {
		t.Fatal("foreign license accepted")
	}
	if _, err = s.AIBinding(ctx, rollbackLicense.ID); err == nil {
		t.Fatal("partial batch survived rollback")
	}
	// Issuance and a plan save serialize at the plan boundary.
	cfg.Enabled = true
	record = &store.AIPlanRecord{Config: cfg, ProfileBinding: profile}
	check(s.SaveAIPlan(ctx, plan.ID, record))
	raceLicense := &model.License{ProductID: p.ID, PlanID: plan.ID, Email: "race@example.invalid", LicenseKey: store.NewID(), Status: model.StatusActive, ValidFrom: time.Now(), ValidUntil: &end}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs <- s.CreateLicense(ctx, raceLicense) }()
	go func() {
		defer wg.Done()
		next := cfg
		next.GroupID = 35
		errs <- s.SaveAIPlan(ctx, plan.ID, &store.AIPlanRecord{Config: next, ProfileBinding: profile})
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		check(err)
	}
	raceBinding, err := s.AIBinding(ctx, raceLicense.ID)
	check(err)
	if raceBinding.Config.GroupID != 35 {
		t.Fatal("concurrent issuance missed plan revision")
	}
	// The worker reconciles all products in its database. Leave no active
	// issuance fixture for later tests' independent fake provider instances.
	check(s.RevokeLicense(ctx, raceLicense.ID))
	ai.Tick(ctx)
}
