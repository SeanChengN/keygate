package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/service"
	"github.com/tabloy/keygate/internal/store"
)

func TestAIAdminScopeAndRedaction(t *testing.T) {
	dsn := os.Getenv("AI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated AI_TEST_DATABASE_URL required")
	}
	s, err := store.New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.RunMigrations("../../db/migrations"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var p model.Product
	if err = s.DB.NewSelect().Model(&p).Where("slug='movaura-studio'").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	aead, err := crypto.NewAESGCM(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	ai := &service.AIService{Store: s, AEAD: aead, Client: service.NewJijiClient()}
	if err = ai.SaveAccount(ctx, p.ID, true, &service.AIAccountTokens{AccessToken: "SYNTHETIC-ACCESS", RefreshToken: "SYNTHETIC-REFRESH", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	h := &AdminHandler{Store: s, AI: ai}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if c.GetHeader("Test-Foreign-Scope") == "1" {
			c.Set("api_key", &model.APIKey{ProductID: "other-product"})
		}
	})
	r.GET("/products/:id/ai-account", h.GetAIAccount)
	r.PUT("/products/:id/ai-account", h.PutAIAccount)
	r.PUT("/plans/:id/ai", h.PutAIPlan)
	r.GET("/plans/:id/ai", h.GetAIPlan)
	r.POST("/inventory", h.CreateInventoryBatch)
	r.GET("/licenses/:id/ai", h.GetLicenseAI)
	r.GET("/licenses/:id/key", h.RevealLicenseKey)
	r.DELETE("/licenses/:id", h.DeleteLicense)
	r.GET("/licenses/:id/deletion", h.GetLicenseDeletion)
	r.POST("/licenses/:id/ai/provision", h.ProvisionLicenseAI)
	r.POST("/plans/:id/ai/provision-batches", h.CreateAIProvisionBatch)
	r.GET("/plans/:id/ai/provision-preview", h.PreviewAIProvision)
	request := func(path string, foreign bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		q := httptest.NewRequest("GET", path, nil)
		if foreign {
			q.Header.Set("Test-Foreign-Scope", "1")
		}
		r.ServeHTTP(w, q)
		return w
	}
	w := request("/products/"+p.ID+"/ai-account", false)
	if !strings.Contains(w.Body.String(), "expires_at") || !strings.Contains(w.Body.String(), "updated_at") {
		t.Fatal("saved metadata missing")
	}
	if w.Code != 200 || strings.Contains(w.Body.String(), "SYNTHETIC") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unsafe metadata: %d", w.Code)
	}
	if w = request("/products/"+p.ID+"/ai-account", true); w.Code != 403 {
		t.Fatal("cross-product account scope accepted")
	}
	var b model.AIBinding
	if err = s.DB.NewSelect().Model(&b).Limit(1).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if w = request("/licenses/"+b.LicenseID+"/ai", true); w.Code != 403 {
		t.Fatal("cross-product license scope accepted")
	}
	for _, path := range []string{"/licenses/" + b.LicenseID + "/ai/provision", "/plans/" + func() string { l, _ := s.FindLicenseByID(ctx, b.LicenseID); return l.PlanID }() + "/ai/provision-batches"} {
		w := httptest.NewRecorder()
		q := httptest.NewRequest("POST", path, strings.NewReader("{}"))
		q.Header.Set("Test-Foreign-Scope", "1")
		r.ServeHTTP(w, q)
		if w.Code != 403 {
			t.Fatalf("cross product provisioning accepted: %d", w.Code)
		}
	}
	w = request("/licenses/"+b.LicenseID+"/ai", false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-") || strings.Contains(w.Body.String(), "candidate_secret") || strings.Contains(w.Body.String(), "\"secret\"") {
		t.Fatal("AI details leaked credential")
	}
	// End-to-end management contract: zero quota is valid, grace remains gated.
	plan := &model.Plan{ProductID: p.ID, Name: "Unlimited handler", Slug: store.NewID(), LicenseType: "subscription", BillingInterval: "year", GraceDays: 7, Active: true, LicenseModel: "standard"}
	if err = s.CreatePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateEntitlement(ctx, &model.Entitlement{PlanID: plan.ID, Feature: "studio.ai_profile", ValueType: "string", Value: `{"provider_id":"jiji","base_url":"https://api-slb.jiji.cc/v1","protocol":"openai"}`}); err != nil {
		t.Fatal(err)
	}
	update := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		q := httptest.NewRequest("PUT", "/plans/"+plan.ID+"/ai", strings.NewReader(body))
		r.ServeHTTP(w, q)
		return w
	}
	body := `{"enabled":false,"group_id":53,"quota":0,"rate_limit_5h":0,"rate_limit_1d":0,"rate_limit_7d":0}`
	if w = update(body); w.Code != 400 || !strings.Contains(w.Body.String(), "AI_PLAN_INELIGIBLE") {
		t.Fatal("grace error not specific")
	}
	if _, err = s.DB.ExecContext(ctx, "UPDATE plans SET grace_days=0 WHERE id=?", plan.ID); err != nil {
		t.Fatal(err)
	}
	if w = update(body); w.Code != 200 {
		t.Fatalf("zero quota rejected: %d", w.Code)
	}
	if w = update(strings.Replace(body, `"quota":0`, `"quota":0.000000001`, 1)); w.Code != 400 || !strings.Contains(w.Body.String(), "AI_QUOTA_INVALID") {
		t.Fatal("tiny positive accepted")
	}
	// Batch readiness and unlimited settings use the same backend rules.
	stockBody, _ := json.Marshal(map[string]any{"product_id": p.ID, "plan_id": plan.ID, "quantity": 1})
	req := httptest.NewRequest("POST", "/inventory", strings.NewReader(string(stockBody)))
	req.Header.Set("Idempotency-Key", store.NewID())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("unlimited inventory rejected: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data store.InventoryBatch `json:"data"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	var stock model.License
	if err = s.DB.NewSelect().Model(&stock).Where("issuance_batch_id=?", created.Data.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := s.AIBinding(ctx, stock.ID)
	if err != nil {
		t.Fatal(err)
	}
	w = request("/licenses/"+stock.ID+"/key", false)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("pending stock reveal rejected")
	}
	after, err := s.AIBinding(ctx, stock.ID)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.FindLicenseByID(ctx, stock.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Status != model.StatusPendingActivation || fresh.ValidUntil != nil || fresh.FirstExportID != "" || after.RemoteID != 0 || !after.Anchor.Equal(before.Anchor) || after.State != "waiting_activation" {
		t.Fatal("reveal caused stock activation or provisioning")
	}
	if w = request("/licenses/"+stock.ID+"/key", true); w.Code != 403 {
		t.Fatal("foreign stock reveal accepted")
	}
	// An unactivated stock key has no remote deletion work and returns 204.
	if err = s.RevokeLicense(ctx, stock.ID); err != nil {
		t.Fatal(err)
	}
	deleteReq := httptest.NewRequest("DELETE", "/licenses/"+stock.ID, nil)
	deleteResp := httptest.NewRecorder()
	r.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != 204 {
		t.Fatalf("keyless stock deletion: %d", deleteResp.Code)
	}
	// Never serialize private credential attachments through ordinary records.
	raw, err := json.Marshal(&b)
	if err != nil || strings.Contains(string(raw), "secret") {
		t.Fatal("binding redaction failed")
	}
}
