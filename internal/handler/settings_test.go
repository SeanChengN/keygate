package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/service"
	"github.com/tabloy/keygate/internal/store"
	"github.com/uptrace/bun"
)

func TestSettingsMapsAreConsistent(t *testing.T) {
	for key := range settingsServerOwned {
		if settingsWritable[key] {
			t.Errorf("%q is server-owned but also writable", key)
		}
		if settingsSecret[key] {
			t.Errorf("%q is server-owned and must not be exposed as a writable secret", key)
		}
	}
	for key := range settingsSecret {
		if !settingsWritable[key] {
			t.Errorf("%q is write-only but not writable", key)
		}
	}
}

func TestGetSettingsOutputIsWritableBack(t *testing.T) {
	stored := map[string]string{
		"site_name":                  "Acme",
		"smtp_password":              "secret",
		"stripe_webhook_secret":      "whsec_live",
		"stripe_webhook_endpoint_id": "we_1",
	}

	for key, val := range stored {
		if settingsServerOwned[key] || settingsSecret[key] {
			continue
		}
		if !settingsWritable[key] {
			t.Errorf("GetSettings would return %q=%q but UpdateSettings rejects it", key, val)
		}
	}

	for _, key := range []string{"smtp_password", "stripe_webhook_secret", "stripe_webhook_endpoint_id"} {
		if !settingsSecret[key] && !settingsServerOwned[key] {
			t.Errorf("%q must never be returned by GetSettings", key)
		}
	}
}

func TestAIAdminSMTPSettings(t *testing.T) {
	dsn := os.Getenv("AI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated AI_TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	root, err := store.New(dsn)
	check(err)
	defer root.Close()
	schema := bun.Ident("test_smtp_" + strings.ReplaceAll(store.NewID(), "-", ""))
	_, err = root.DB.ExecContext(ctx, "CREATE SCHEMA ?", schema)
	check(err)
	defer root.DB.ExecContext(ctx, "DROP SCHEMA ? CASCADE", schema)
	scopedDSN, err := url.Parse(dsn)
	check(err)
	params := scopedDSN.Query()
	params.Set("search_path", string(schema))
	scopedDSN.RawQuery = params.Encode()
	db, err := store.New(scopedDSN.String())
	check(err)
	defer db.Close()
	check(db.RunMigrations("../../db/migrations"))

	owner := &model.User{Role: model.RoleOwner, Email: "owner@example.test"}
	admin := &model.User{Role: model.RoleAdmin, Email: "admin@example.test"}
	check(db.UpsertUser(ctx, owner))
	check(db.UpsertUser(ctx, admin))
	key, err := crypto.NewAESGCM(make([]byte, 32))
	check(err)
	mailer := service.NewEmailService("", "", "", "", "", slog.Default(), db)
	check(mailer.ConfigureSMTPEncryption(ctx, key, nil))
	h := &AdminHandler{Store: db, Email: mailer}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Authenticate requests as the selected fixture user. The real router
	// already restricts these endpoints to the admin scope.
	router.Use(func(c *gin.Context) {
		user := owner
		if c.GetHeader("Fixture-Role") == model.RoleAdmin {
			user = admin
		}
		c.Set("user_id", user.ID)
		c.Set("email", user.Email)
	})
	router.GET("/settings", h.GetSettings)
	router.PUT("/settings", h.UpdateSettings)
	router.DELETE("/settings/secrets/:key", h.ClearSecretSetting)
	router.POST("/settings/test-email", h.SendTestEmail)
	request := func(method, path, role, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Fixture-Role", role)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		if recorder.Code != want {
			t.Fatalf("%s %s: got %d, want %d (%s)", method, path, recorder.Code, want, recorder.Body.String())
		}
		return recorder
	}

	legacy := map[string]string{"smtp_host": "smtp.example.test", "smtp_port": "465",
		"smtp_from": "sender@example.test", "smtp_username": "sender", "smtp_password": "synthetic-unexposed-code"}
	check(db.SetSettings(ctx, legacy))
	response := request(http.MethodPost, "/settings/test-email", model.RoleOwner, "", http.StatusServiceUnavailable)
	if !strings.Contains(response.Body.String(), "EMAIL_NOT_CONFIGURED") {
		t.Fatal("test mail used legacy settings before owner confirmation")
	}
	request(http.MethodPut, "/settings", model.RoleAdmin, `{"settings":{"smtp_host":"smtp.example.test"}}`, http.StatusForbidden)
	request(http.MethodPut, "/settings", model.RoleOwner, `{"settings":{"smtp_port":"70000"}}`, http.StatusBadRequest)
	request(http.MethodPut, "/settings", model.RoleAdmin, `{"settings":{"site_name":"Updated site name"}}`, http.StatusOK)
	request(http.MethodPut, "/settings", model.RoleOwner, `{"settings":{"smtp_host":"smtp.example.test","smtp_password":""}}`, http.StatusOK)

	stored, err := db.GetSettings(ctx)
	check(err)
	if stored[service.SMTPPasswordCipherKey] == "" || !mailer.IsConfigured() {
		t.Fatal("owner confirmation did not activate encrypted settings")
	}
	response = request(http.MethodGet, "/settings", model.RoleAdmin, "", http.StatusOK)
	body := response.Body.String()
	if strings.Contains(body, stored[service.SMTPPasswordCipherKey]) || strings.Contains(body, legacy["smtp_password"]) ||
		strings.Contains(body, service.SMTPConfirmedKey) || strings.Contains(body, service.SMTPPasswordCipherKey) ||
		!strings.Contains(body, `"smtp_password":true`) || !strings.Contains(body, `"smtp_source":"dashboard"`) {
		t.Fatal("settings did not report safe configuration metadata")
	}
	request(http.MethodDelete, "/settings/secrets/smtp_password", model.RoleAdmin, "", http.StatusForbidden)
	request(http.MethodDelete, "/settings/secrets/smtp_password", model.RoleOwner, "", http.StatusOK)
	response = request(http.MethodPost, "/settings/test-email", model.RoleOwner, "", http.StatusServiceUnavailable)
	if !strings.Contains(response.Body.String(), "EMAIL_INVALID_SETTINGS") {
		t.Fatal("cleared password did not invalidate authentication")
	}
}
