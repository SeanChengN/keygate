package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/tabloy/keygate/internal/crypto"
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
	p := &model.Product{Name: "Stock AI", Slug: "movaura-studio", Type: "desktop"}
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
	cfg := &store.AIPlanRecord{Config: model.AIPlanSettings{GroupID: 53, Quota: .01}, ProfileBinding: profile}
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
	ai := &AIService{Store: s, AEAD: aead, Client: &JijiClient{BaseURL: server.URL, HTTP: server.Client()}, Email: &EmailService{enabled: true}}
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
