package handler

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/service"
	"github.com/tabloy/keygate/internal/store"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAIInventoryMetadataAndTXT(t *testing.T) {
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
	p := &model.Product{Name: "Inventory handler", Slug: "movaura-studio", Type: "desktop"}
	if s.DB.NewSelect().Model(p).Where("slug='movaura-studio'").Scan(ctx) != nil {
		check(s.CreateProduct(ctx, p))
	}
	plan := &model.Plan{ProductID: p.ID, Name: "Inventory handler", Slug: store.NewID(), LicenseType: "subscription", BillingInterval: "year", LicenseModel: "standard", MaxActivations: 1, GraceDays: 0, Active: true}
	check(s.CreatePlan(ctx, plan))
	cfg := &store.AIPlanRecord{Config: model.AIPlanSettings{GroupID: 53, Quota: .01}, ProfileBinding: "synthetic"}
	check(s.SaveAIPlan(ctx, plan.ID, cfg))
	b := &store.InventoryBatch{ProductID: p.ID, PlanID: plan.ID, ActorID: "test-admin", RequestKey: store.NewID(), Quantity: 2}
	check(s.CreateInventory(ctx, b, cfg))
	aead, err := crypto.NewAESGCM(make([]byte, 32))
	check(err)
	h := &AdminHandler{Store: s, AI: &service.AIService{Store: s, AEAD: aead}}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("admin_id", "test-admin")
		if c.GetHeader("Foreign") == "1" {
			c.Set("api_key", &model.APIKey{ProductID: "foreign"})
		}
	})
	router.GET("/batches/:id", h.GetInventoryBatch)
	router.POST("/exports", h.CreateKeyExport)
	router.GET("/exports/:id", h.DownloadKeyExport)
	router.DELETE("/plans/:id", h.DeletePlan)
	call := func(method, path, body string, foreign bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Idempotency-Key", store.NewID())
		if foreign {
			r.Header.Set("Foreign", "1")
		}
		router.ServeHTTP(w, r)
		return w
	}
	w := call("GET", "/batches/"+b.ID, "", false)
	if w.Code != 200 || strings.Contains(w.Body.String(), "license_key") || strings.Contains(w.Body.String(), "secret") {
		t.Fatal("batch leaked credentials")
	}
	if call("GET", "/batches/"+b.ID, "", true).Code != 403 {
		t.Fatal("cross product batch visible")
	}
	var rows []*model.License
	check(s.DB.NewSelect().Model(&rows).Where("issuance_batch_id=?", b.ID).Order("id").Scan(ctx))
	body, _ := json.Marshal(map[string]any{"license_ids": []string{rows[0].ID, rows[1].ID}})
	w = call("POST", "/exports", string(body), true)
	if w.Code != 403 {
		t.Fatal("cross product export accepted")
	}
	w = call("POST", "/exports", string(body), false)
	if w.Code != 201 {
		t.Fatalf("export creation: %d", w.Code)
	}
	var envelope struct {
		Data store.KeyExport `json:"data"`
	}
	check(json.Unmarshal(w.Body.Bytes(), &envelope))
	w = call("GET", "/exports/"+envelope.Data.ID, "", false)
	expected := s.DecryptLicenseKey(rows[0]) + "\n" + s.DecryptLicenseKey(rows[1]) + "\n"
	if w.Code != 200 || w.Body.String() != expected || strings.HasPrefix(w.Body.String(), "\ufeff") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("TXT contract mismatch")
	}
	// Original manifest still works after activation; no silent omissions.
	_, err = s.DB.ExecContext(ctx, "UPDATE licenses SET status='active',valid_until=? WHERE id=?", time.Now().Add(time.Hour), rows[0].ID)
	check(err)
	if w = call("GET", "/exports/"+envelope.Data.ID, "", false); w.Code != 200 || w.Body.String() != expected {
		t.Fatal("historical download changed")
	}
	if call("DELETE", "/plans/"+plan.ID, "", false).Code != 409 {
		t.Fatal("plan with inventory not protected")
	}
	// Remove only isolated fixture records through the same safe delete workflow.
	_, err = s.DB.ExecContext(ctx, "UPDATE licenses SET status='revoked' WHERE issuance_batch_id=?", b.ID)
	check(err)
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET state='done' WHERE license_id IN (SELECT id FROM licenses WHERE issuance_batch_id=?)", b.ID)
	check(err)
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_bindings SET state='synced',remote_view='{\"status\":\"inactive\"}' WHERE license_id IN (SELECT id FROM licenses WHERE issuance_batch_id=?)", b.ID)
	check(err)
	for _, l := range rows {
		_, err = s.DeleteRevokedUnpaidLicense(ctx, l.ID, "test-admin", "")
		check(err)
	}
	if call("DELETE", "/plans/"+plan.ID, "", false).Code != 204 {
		t.Fatal("AI-configured empty plan deletion failed")
	}
	if call("GET", "/exports/"+envelope.Data.ID, "", false).Code != 409 {
		t.Fatal("missing historical row silently omitted")
	}
}
