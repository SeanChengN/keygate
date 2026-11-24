package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/license"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAIInventoryProofAndAsyncDelivery(t *testing.T) {
	dsn := os.Getenv("AI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated database required")
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
	p := &model.Product{Name: "Stock AI", Slug: "movaura-studio", Type: "desktop", InventoryEnabled: true, PackageAIEnabled: true}
	if s.DB.NewSelect().Model(p).Where("slug='movaura-studio'").Scan(ctx) != nil {
		check(s.CreateProduct(ctx, p))
	}
	plan := &model.Plan{ProductID: p.ID, Name: "Stock synthetic", Slug: store.NewID(), LicenseType: "subscription", BillingInterval: "month", LicenseModel: "standard", MaxActivations: 1, GraceDays: 0, Active: true}
	check(s.CreatePlan(ctx, plan))
	check(s.CreateEntitlement(ctx, &model.Entitlement{PlanID: plan.ID, Feature: "studio.ai_profile", ValueType: "string", Value: `{"provider_id":"jiji","base_url":"https://api-slb.jiji.cc/v1","protocol":"openai"}`}))
	plan, err = s.FindPlanByID(ctx, plan.ID)
	check(err)
	profile, err := AIProfileBinding(plan)
	check(err)
	cfg := &store.AIPlanRecord{Config: model.AIPlanSettings{GroupID: 53, Quota: 0}, ProfileBinding: profile}
	check(s.SaveAIPlan(ctx, plan.ID, cfg))
	batch := &store.InventoryBatch{ProductID: p.ID, PlanID: plan.ID, ActorID: "test", RequestKey: store.NewID(), Quantity: 1}
	check(s.CreateInventory(ctx, batch, cfg))
	l := new(model.License)
	check(s.DB.NewSelect().Model(l).Where("issuance_batch_id=?", batch.ID).Scan(ctx))
	fake := &fakeAI{keys: map[int64]*JijiKey{}}
	server := httptest.NewServer(fake)
	defer server.Close()
	aead, err := crypto.NewAESGCM(make([]byte, 32))
	check(err)
	ai := &AIService{Store: s, AEAD: aead, Client: &JijiClient{BaseURL: server.URL, HTTP: server.Client()}, Email: &EmailService{environment: smtpConfig{host: "smtp.example.test", from: "noreply@example.test"}}}
	check(ai.SaveAccount(ctx, p.ID, true, &AIAccountTokens{"synthetic", "synthetic-refresh", time.Now().Add(time.Hour).Unix()}))
	ai.Tick(ctx)
	if fake.creates != 0 {
		t.Fatal("unactivated stock provisioned")
	}
	_, signer, err := ed25519.GenerateKey(rand.Reader)
	check(err)
	svc := NewLicenseService(s, signer, slog.Default(), nil, nil)
	svc.AI = ai
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	check(err)
	key := s.DecryptLicenseKey(l)
	input := func() ActivateInput {
		nonce := make([]byte, 16)
		_, err = rand.Read(nonce)
		check(err)
		n := base64.RawURLEncoding.EncodeToString(nonce)
		stamp := time.Now().Unix()
		message := strings.Join([]string{"wms-device-proof:v1", "activate", key, "test-stock-device", p.ID, "", "", "", fmt.Sprint(stamp), n}, "\n")
		return ActivateInput{LicenseKey: key, ProductID: p.ID, Identifier: "test-stock-device", IdentifierType: "device", DeviceProof: DeviceProofInput{PublicKey: base64.RawURLEncoding.EncodeToString(pub), Timestamp: stamp, Nonce: n, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(message)))}}
	}
	bad := input()
	bad.DeviceProof.Signature = "invalid"
	if _, err = svc.Activate(ctx, bad); err == nil {
		t.Fatal("invalid proof accepted")
	}
	l, err = s.FindLicenseByID(ctx, l.ID)
	check(err)
	if l.Status != model.StatusPendingActivation {
		t.Fatal("invalid proof started clock")
	}
	result, err := svc.Activate(ctx, input())
	check(err)
	if result.Status != "activation_pending" || result.Token != "" {
		t.Fatal("incomplete lease was issued")
	}
	ai.Tick(ctx)
	result, err = svc.Activate(ctx, input())
	check(err)
	if result.Token == "" {
		t.Fatal("ready credential not published")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.Split(result.Token, ".")[0])
	var claims map[string]any
	check(json.Unmarshal(raw, &claims))
	if claims["ai_credential"] == nil {
		t.Fatal("signed credential missing")
	}
	if len(fake.keys) != 1 || fake.keys[1].Quota != 0 {
		t.Fatal("inventory lost unlimited quota")
	}
	if fake.creates != 1 {
		t.Fatal("duplicate provider key")
	}
	var emails int
	check(s.DB.NewRaw("SELECT count(*) FROM email_queue WHERE id=?", "ai-license-issued:"+l.ID).Scan(ctx, &emails))
	if emails != 0 {
		t.Fatal("anonymous inventory emailed")
	}
	// Retain isolated fixtures but do not leave runnable work for subsequent fake-provider tests.
	check(s.RevokeLicense(ctx, l.ID))
	ai.Tick(ctx)
}

func TestDayInventoryWithAndWithoutAI(t *testing.T) {
	dsn := os.Getenv("AI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated database required")
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
	p := &model.Product{Name: "WMS test", Slug: store.NewID(), Type: "hybrid"}
	check(s.CreateProduct(ctx, p))
	plan := &model.Plan{ProductID: p.ID, Name: "7 day", Slug: store.NewID(), LicenseType: "subscription", BillingInterval: "day", BillingDays: 7, LicenseModel: "standard", MaxActivations: 1, Active: true}
	if err := s.CreatePlan(ctx, plan); !errors.Is(err, store.ErrProductInventoryType) {
		t.Fatalf("product without inventory accepted day plan: %v", err)
	}
	on, off := true, false
	check(s.UpdateProduct(ctx, p, &on, &on, "test"))
	check(s.CreatePlan(ctx, plan))
	check(s.CreateEntitlement(ctx, &model.Entitlement{PlanID: plan.ID, Feature: "license.ai_profile", ValueType: "string", Value: `{"provider_id":"jiji","base_url":"https://api-slb.jiji.cc/v1","protocol":"openai"}`}))
	plan, err = s.FindPlanByID(ctx, plan.ID)
	check(err)
	profile, err := AIProfileBinding(plan)
	check(err)
	cfg := &store.AIPlanRecord{Config: model.AIPlanSettings{GroupID: 53, Quota: 1}, ProfileBinding: profile}
	check(s.SaveAIPlan(ctx, plan.ID, cfg))
	fake := &fakeAI{keys: map[int64]*JijiKey{}}
	server := httptest.NewServer(fake)
	defer server.Close()
	aead, err := crypto.NewAESGCM(make([]byte, 32))
	check(err)
	ai := &AIService{Store: s, AEAD: aead, Client: &JijiClient{BaseURL: server.URL, HTTP: server.Client()}}
	check(ai.SaveAccount(ctx, p.ID, true, &AIAccountTokens{"fake", "fake-refresh", time.Now().Add(time.Hour).Unix()}))
	newBatch := func(settings *store.AIPlanRecord) (*store.InventoryBatch, *model.License) {
		t.Helper()
		b := &store.InventoryBatch{ProductID: p.ID, PlanID: plan.ID, ActorID: "test", RequestKey: store.NewID(), Quantity: 1}
		check(s.CreateInventory(ctx, b, settings))
		l := new(model.License)
		check(s.DB.NewSelect().Model(l).Where("issuance_batch_id=?", b.ID).Scan(ctx))
		return b, l
	}
	batch, bare := newBatch(nil)
	_, enrolled := newBatch(cfg)
	check(s.UpdateProduct(ctx, p, &off, &off, "test"))
	check(s.CreateInventory(ctx, batch, nil))
	if err := s.CreateInventory(ctx, &store.InventoryBatch{ProductID: p.ID, PlanID: plan.ID, ActorID: "test", RequestKey: store.NewID(), Quantity: 1}, nil); !errors.Is(err, store.ErrInventoryIneligible) {
		t.Fatalf("disabled inventory accepted new batch: %v", err)
	}
	if _, err := ai.ProvisionConfig(ctx, plan.ID); err == nil {
		t.Fatal("disabled AI accepted new provisioning")
	}
	e := &store.KeyExport{ActorID: "test", RequestKey: store.NewID(), LicenseIDs: []string{bare.ID}}
	check(s.CreateKeyExport(ctx, e))
	plan.BillingDays = 14
	check(s.UpdatePlan(ctx, plan))
	_, signer, err := ed25519.GenerateKey(rand.Reader)
	check(err)
	svc := NewLicenseService(s, signer, slog.Default(), nil, nil)
	svc.AI = ai
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	check(err)
	input := func(l *model.License) ActivateInput {
		key := s.DecryptLicenseKey(l)
		nonce := make([]byte, 16)
		_, err := rand.Read(nonce)
		check(err)
		n := base64.RawURLEncoding.EncodeToString(nonce)
		stamp := time.Now().Unix()
		message := strings.Join([]string{"wms-device-proof:v1", "activate", key, "synthetic-device", p.ID, "", "", "", fmt.Sprint(stamp), n}, "\n")
		return ActivateInput{LicenseKey: key, ProductID: p.ID, Identifier: "synthetic-device",
			DeviceProof: DeviceProofInput{PublicKey: base64.RawURLEncoding.EncodeToString(pub), Timestamp: stamp, Nonce: n, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(message)))}}
	}
	activate := func(l *model.License, withAI bool) *model.License {
		t.Helper()
		first, err := svc.Activate(ctx, input(l))
		check(err)
		if withAI {
			if first.Status != "activation_pending" || first.Token != "" {
				t.Fatal("promised AI did not wait for key")
			}
			ai.Tick(ctx)
		} else if first.Status != "activated" || first.Token == "" {
			t.Fatal("licensing-only stock waited for AI")
		}
		l, err = s.FindLicenseByID(ctx, l.ID)
		check(err)
		if l.ValidUntil == nil || l.ValidUntil.Sub(l.ValidFrom) != 7*24*time.Hour {
			t.Fatalf("inventory did not preserve its seven-day snapshot: %+v", l.ValidUntil)
		}
		second, err := svc.Activate(ctx, input(l))
		check(err)
		var claims license.VerifyToken
		raw, err := base64.RawURLEncoding.DecodeString(strings.Split(second.Token, ".")[0])
		check(err)
		check(json.Unmarshal(raw, &claims))
		if claims.BillingDays != 7 || claims.ValidUntil == nil || *claims.ValidUntil != l.ValidUntil.Unix() || claims.ExpiresAt > *claims.ValidUntil || (claims.AICredential != nil) != withAI {
			t.Fatal("signed term, lease or credential inconsistent with inventory")
		}
		return l
	}
	bare = activate(bare, false)
	enrolled = activate(enrolled, true)
	if len(fake.keys) != 1 || fake.keys[1].ExpiresAt == nil || !fake.keys[1].ExpiresAt.Equal(*enrolled.ValidUntil) {
		t.Fatal("remote expiry differs from the license")
	}
	if err := s.SetLicenseValidUntil(ctx, bare.ID, bare.ValidUntil, true); !errors.Is(err, store.ErrDayLicenseTermFixed) {
		t.Fatalf("day term update = %v", err)
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_bindings SET anchor=? WHERE license_id=?", time.Now().AddDate(0, -2, 0), enrolled.ID)
	check(err)
	fake.keys[1].QuotaUsed = .35
	ai.Tick(ctx)
	if fake.resets != 0 {
		t.Fatal("day quota was reset monthly")
	}
	check(store.EnqueueAI(ctx, s.DB, enrolled.ID, "rotate", "day-rotate"))
	ai.Tick(ctx)
	if len(fake.keys) != 2 || fake.keys[2].Quota != .65 || fake.keys[1].Status != "inactive" {
		t.Fatal("replacement did not preserve remaining allowance")
	}
	check(store.EnqueueAI(ctx, s.DB, enrolled.ID, "reset_quota", "day-reset"))
	ai.Tick(ctx)
	if fake.resets != 1 || fake.keys[2].Quota != 1 || !fake.keys[2].ExpiresAt.Equal(*enrolled.ValidUntil) {
		t.Fatal("manual reset did not restore only the original allowance")
	}
	expired := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	_, err = s.DB.ExecContext(ctx, "UPDATE licenses SET valid_until=? WHERE id=?", expired, enrolled.ID)
	check(err)
	if _, err := svc.Activate(ctx, input(enrolled)); err == nil {
		t.Fatal("expired inventory still issued an online lease")
	}
	ai.Tick(ctx)
	if fake.keys[2].Status != "inactive" || fake.keys[2].ExpiresAt == nil || !fake.keys[2].ExpiresAt.Equal(expired) {
		t.Fatal("AI key did not expire with its license")
	}
}
