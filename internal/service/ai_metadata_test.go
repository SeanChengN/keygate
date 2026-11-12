package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAIAccountMetadataNeverRefreshes(t *testing.T) {
	dsn := os.Getenv("AI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated database required")
	}
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
	ctx := context.Background()
	check(s.RunMigrations("../../db/migrations"))
	p := &model.Product{Name: "Metadata synthetic", Slug: "metadata-" + store.NewID(), Type: "desktop"}
	check(s.CreateProduct(ctx, p))
	aead, err := crypto.NewAESGCM(make([]byte, 32))
	check(err)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	ai := &AIService{Store: s, AEAD: aead, Client: &JijiClient{BaseURL: server.URL, HTTP: server.Client()}}
	m, err := ai.AccountMetadata(ctx, p.ID)
	check(err)
	if m.Configured || m.ExpiresAt != nil || m.State != "unconfigured" {
		t.Fatal("unconfigured metadata incorrect")
	}
	expiry := time.Now().Add(time.Hour).Unix()
	check(ai.SaveAccount(ctx, p.ID, true, &AIAccountTokens{"PRIVATE-SYNTHETIC-ACCESS", "PRIVATE-SYNTHETIC-REFRESH", expiry}))
	// A new service instance simulates reopening after a server restart.
	restarted := &AIService{Store: s, AEAD: aead, Client: ai.Client}
	m, err = restarted.AccountMetadata(ctx, p.ID)
	check(err)
	raw, _ := json.Marshal(m)
	if !m.Configured || m.ExpiresAt == nil || *m.ExpiresAt != expiry || m.UpdatedAt == nil || strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("unsafe or missing metadata")
	}
	check(ai.SaveAccount(ctx, p.ID, false, nil))
	m, err = restarted.AccountMetadata(ctx, p.ID)
	check(err)
	if m.Enabled || *m.ExpiresAt != expiry {
		t.Fatal("blank save replaced credentials")
	}
	if ai.SaveAccount(ctx, p.ID, true, &AIAccountTokens{"replacement", "", expiry}) == nil {
		t.Fatal("partial replacement accepted")
	}
	m, err = ai.AccountMetadata(ctx, p.ID)
	check(err)
	if m.Enabled || *m.ExpiresAt != expiry {
		t.Fatal("failed replacement changed saved account")
	}
	// Expired metadata is still readable; GET must never rotate a refresh token.
	rawSecret, err := ai.seal(AIAccountTokens{"old", "refresh", time.Now().Add(-time.Hour).Unix()}, "account:"+p.ID)
	check(err)
	_, err = s.DB.ExecContext(ctx, "UPDATE ai_provider_accounts SET secret=? WHERE product_id=?", rawSecret, p.ID)
	check(err)
	m, err = restarted.AccountMetadata(ctx, p.ID)
	check(err)
	if *m.ExpiresAt >= time.Now().Unix() || calls != 0 {
		t.Fatal("metadata triggered an upstream request")
	}
}

func TestAIErrorCodesDoNotExposeUnknownDetails(t *testing.T) {
	for _, tc := range []struct{ message, code string }{
		{"AI requires a subscription with zero grace", "AI_PLAN_INELIGIBLE"},
		{"jiji account is not configured", "AI_ACCOUNT_UNCONFIGURED"},
		{"reset result unknown; automatic replay refused", "AI_RESULT_UNCERTAIN"},
		{"PRIVATE-SYNTHETIC-ERROR", "AI_OPERATION_FAILED"},
	} {
		if AIErrorCode(errors.New(tc.message)) != tc.code {
			t.Fatalf("classification: %s", tc.code)
		}
	}
	if AIErrorCode(&AIProviderError{Status: 429, Code: "insufficient_quota"}) != "insufficient_quota" {
		t.Fatal("quota response misclassified")
	}
	if AIErrorCode(&AIProviderError{Status: 429, Code: "rejected"}) != "AI_PROVIDER_RATE_LIMITED" {
		t.Fatal("rate limit misclassified")
	}
}
