package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
	"github.com/uptrace/bun"
)

func TestAIAccountMonitorAndSMTPDelivery(t *testing.T) {
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
	schema := bun.Ident("test_ai_health_" + strings.ReplaceAll(store.NewID(), "-", ""))
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

	p := &model.Product{Name: "Synthetic studio <fixture>", Slug: "synthetic-ai-health", Type: "desktop"}
	check(db.CreateProduct(ctx, p))
	for _, u := range []*model.User{
		{Email: "owner-one@example.test", Role: model.RoleOwner},
		{Email: "owner-two@example.test", Role: model.RoleOwner},
		{Email: "admin@example.test", Role: model.RoleAdmin},
		{Email: "customer@example.test", Role: model.RoleUser},
	} {
		check(db.UpsertUser(ctx, u))
	}
	key, err := crypto.NewAESGCM(make([]byte, 32))
	check(err)
	email := NewEmailService("", "", "", "", "", slog.Default(), db)
	check(email.ConfigureSMTPEncryption(ctx, key, nil))

	var refreshes, providerStatus atomic.Int32
	var seenToken atomic.Value
	jiji := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]string
		if r.Method != "POST" || r.URL.Path != "/api/v1/auth/refresh" || json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid refresh call")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		seenToken.Store(request["refresh_token"])
		count := refreshes.Add(1)
		switch status := providerStatus.Load(); status {
		case -1:
			<-r.Context().Done()
		case 0:
			fmt.Fprintf(w, `{"code":0,"data":{"access_token":"PRIVATE-ACCESS-%d","refresh_token":"PRIVATE-REFRESH-%d","expires_in":86400}}`, count, count)
		default:
			w.WriteHeader(int(status))
			fmt.Fprint(w, `{"message":"PRIVATE-UPSTREAM-RESPONSE"}`)
		}
	}))
	defer jiji.Close()
	client := jiji.Client()
	client.Timeout = 500 * time.Millisecond
	ai := &AIService{Store: db, AEAD: key, Client: &JijiClient{BaseURL: jiji.URL, HTTP: client}, Email: email}
	save := func(enabled bool, refresh string, expires time.Duration) {
		t.Helper()
		check(ai.SaveAccount(ctx, p.ID, enabled, &AIAccountTokens{"PRIVATE-ACCESS", refresh, time.Now().Add(expires).Unix()}))
	}
	expire := func(account *aiAccount) {
		t.Helper()
		account.ExpiresAt = time.Now().Add(-time.Minute).Unix()
		raw, err := ai.seal(account.AIAccountTokens, "account:"+p.ID)
		check(err)
		_, err = db.DB.ExecContext(ctx, "UPDATE ai_provider_accounts SET secret = ? WHERE product_id = ?", raw, p.ID)
		check(err)
	}
	status := func(state, failure, alert string) *AIAccountMetadata {
		t.Helper()
		m, err := ai.AccountMetadata(ctx, p.ID)
		check(err)
		if m.State != state || m.HealthError != failure || m.AlertStatus != alert {
			t.Fatalf("metadata state/error/alert = %s/%s/%s, want %s/%s/%s", m.State, m.HealthError, m.AlertStatus, state, failure, alert)
		}
		return m
	}

	save(false, "disabled", time.Minute)
	ai.inspectAccounts(ctx)
	if refreshes.Load() != 0 {
		t.Fatal("disabled account called Jiji")
	}

	save(true, "initial", time.Minute)
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ai.StartAccountMonitor(monitorCtx)
	}()
	defer func() {
		stopMonitor()
		<-monitorDone
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		metadata, err := ai.AccountMetadata(ctx, p.ID)
		check(err)
		if metadata.LastRefreshAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup monitor did not renew the idle account")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stopMonitor()
	<-monitorDone
	first, err := ai.account(ctx, p.ID, false)
	check(err)
	if refreshes.Load() != 1 || seenToken.Load() != "initial" || first.RefreshToken != "PRIVATE-REFRESH-1" {
		t.Fatal("startup did not persist the rotated token pair")
	}

	expire(first)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ai.inspectAccounts(ctx)
	}()
	go func() {
		defer wg.Done()
		if _, err := ai.Account(ctx, p.ID); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	if refreshes.Load() != 2 || seenToken.Load() != "PRIVATE-REFRESH-1" {
		t.Fatal("concurrent inspection and use did not renew exactly once with the stored token")
	}
	ai.noteAccountAuthFailure(ctx, p.ID, first)
	status("ready", "", "")

	current, err := ai.account(ctx, p.ID, false)
	check(err)
	expire(current)
	providerStatus.Store(http.StatusUnauthorized)
	ai.inspectAccounts(ctx)
	status("attention", "refresh_rejected", "waiting_smtp")
	restarted := &AIService{Store: db, AEAD: key, Client: ai.Client, Email: email}
	restarted.inspectAccounts(ctx)
	email.processQueue(ctx, db)
	if refreshes.Load() != 3 {
		t.Fatal("rejected token was replayed")
	}
	var recipients []string
	check(db.DB.NewRaw("SELECT to_addr FROM email_queue WHERE ai_alert_id IS NOT NULL AND status = 'pending' AND attempts = 0 ORDER BY to_addr").Scan(ctx, &recipients))
	if !reflect.DeepEqual(recipients, []string{"owner-one@example.test", "owner-two@example.test"}) {
		t.Fatalf("unexpected recipients or pending attempts: %v", recipients)
	}

	smtp := newMockSMTP(t, mockSMTPConfig{advertiseAuth: "LOGIN", wantUsername: "mailer", wantPassword: "synthetic-code"})
	defer smtp.Close()
	email.tlsConfig = &tls.Config{ServerName: "127.0.0.1", InsecureSkipVerify: true} // local fixture
	settings := map[string]string{"smtp_host": "127.0.0.1", "smtp_port": fmt.Sprint(smtp.Port()),
		"smtp_from": "sender@example.test", "smtp_username": "mailer", "smtp_password": "synthetic-code"}
	check(db.SetSettings(ctx, settings))
	values, err := db.GetSettings(ctx)
	check(err)
	if email.SMTPSource(values) != "unconfirmed" || email.IsConfigured() {
		t.Fatal("legacy settings activated before owner confirmation")
	}
	check(email.SaveSMTPSettings(ctx, map[string]string{"smtp_host": "127.0.0.1", "smtp_password": ""}))
	values, err = db.GetSettings(ctx)
	check(err)
	if email.SMTPSource(values) != "dashboard" || values[smtpPasswordKey] != "" ||
		values[SMTPPasswordCipherKey] == "" || strings.Contains(values[SMTPPasswordCipherKey], "synthetic-code") {
		t.Fatal("dashboard config or password migration failed")
	}
	envEmail := NewEmailService("smtp.example.test", "587", "environment-user", "environment-code", "env@example.test", slog.Default(), db)
	active, err := envEmail.effectiveSMTP(ctx)
	check(err)
	if envEmail.SMTPSource(values) != "environment" || active.from != "env@example.test" || active.password != "environment-code" {
		t.Fatal("saved values overrode the configured environment")
	}

	// A transport failure increments the queued attempt only once and keeps it
	// pending. Fixing SMTP on the running service allows the next attempt.
	check(email.SaveSMTPSettings(ctx, map[string]string{"smtp_password": "wrong-code"}))
	email.processQueue(ctx, db)
	var retried int
	check(db.DB.NewRaw(`SELECT count(*) FROM email_queue
        WHERE status = 'pending' AND attempts = 1 AND next_retry > now() AND error = 'EMAIL_AUTH_FAILED'`).Scan(ctx, &retried))
	if retried != 2 {
		t.Fatalf("expected two scheduled email retries, got %d", retried)
	}
	check(email.SaveSMTPSettings(ctx, map[string]string{"smtp_password": "synthetic-code"}))
	_, err = db.DB.ExecContext(ctx, "UPDATE email_queue SET next_retry = now() WHERE status = 'pending'")
	check(err)
	check(email.SendConfigured(ctx, "owner-one@example.test", "Keygate test email", "test body"))
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			email.processQueue(ctx, db)
		}()
	}
	wg.Wait()
	status("attention", "refresh_rejected", "sent")
	smtp.mu.Lock()
	messages := append([]string(nil), smtp.deliveries...)
	smtp.mu.Unlock()
	if len(messages) != 3 {
		t.Fatalf("SMTP inbox received %d messages, want 3 (test email and two Owner alerts)", len(messages))
	}
	for _, owner := range recipients {
		delivered := false
		for _, message := range messages[1:] {
			delivered = delivered || strings.Contains(message, "To: "+owner)
			if !strings.Contains(message, "Synthetic studio &lt;fixture&gt;") ||
				!strings.Contains(message, emailFooter()) || strings.Contains(message, "PRIVATE-") {
				t.Fatal("delivery is missing expected details or contains private data")
			}
		}
		if !delivered {
			t.Fatalf("SMTP inbox did not receive the alert for %s", owner)
		}
	}

	save(true, "replacement", time.Hour)
	status("ready", "", "")
	providerStatus.Store(-1)
	save(true, "timeout", time.Minute)
	ai.inspectAccounts(ctx)
	status("refresh_uncertain", "refresh_result_unknown", "pending")
	restarted.inspectAccounts(ctx)
	if refreshes.Load() != 4 {
		t.Fatal("uncertain refresh was replayed")
	}
	save(true, "interrupted", time.Minute)
	_, err = db.DB.ExecContext(ctx, "UPDATE ai_provider_accounts SET state = 'refreshing' WHERE product_id = ?", p.ID)
	check(err)
	restarted.inspectAccounts(ctx)
	status("refresh_uncertain", "refresh_interrupted", "pending")
	if refreshes.Load() != 4 {
		t.Fatal("interrupted refresh was replayed")
	}
	providerStatus.Store(0)
	save(true, "new-pair", time.Minute)
	restarted.inspectAccounts(ctx)
	if m := status("ready", "", ""); m.LastRefreshAt == nil || refreshes.Load() != 5 || seenToken.Load() != "new-pair" {
		t.Fatal("new credentials did not resume unattended renewal")
	}
}
