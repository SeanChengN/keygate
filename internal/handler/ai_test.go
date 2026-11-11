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
	r.GET("/licenses/:id/ai", h.GetLicenseAI)
	r.GET("/licenses/:id/key", h.RevealLicenseKey)
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
	// Never serialize private credential attachments through ordinary records.
	raw, err := json.Marshal(&b)
	if err != nil || strings.Contains(string(raw), "secret") {
		t.Fatal("binding redaction failed")
	}
}
