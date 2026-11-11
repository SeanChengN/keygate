package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
)

func TestAIMonthCycle(t *testing.T) {
	plan := &model.Plan{Entitlements: []*model.Entitlement{{Feature: "studio.ai_profile", Value: `{"provider_id":"jiji","base_url":"https://api-slb.jiji.cc/v1","protocol":"openai"}`}}}
	if hash, err := AIProfileBinding(plan); err != nil || hash != "7eef65cec0d8d147c617acbb2c3a07477ecee60dc63e5a0f5798c14e10d81781" {
		t.Fatal("Python binding contract mismatch")
	}
	anchor := time.Date(2024, 1, 31, 18, 12, 3, 0, time.UTC)
	for _, tc := range []struct {
		now   string
		index string
		next  string
	}{
		{"2024-02-29T18:12:02Z", "0", "2024-02-29T18:12:03Z"},
		{"2024-02-29T18:12:03Z", "1", "2024-03-31T18:12:03Z"},
		{"2024-03-31T18:12:03Z", "2", "2024-04-30T18:12:03Z"},
		{"2025-02-28T18:12:03Z", "13", "2025-03-31T18:12:03Z"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		index, next, err := MonthCycle(anchor, now, "UTC")
		if err != nil || index != tc.index || next.Format(time.RFC3339) != tc.next {
			t.Fatalf("%s: %s %s %v", tc.now, index, next, err)
		}
	}
	now := anchor.AddDate(0, 3, 0)
	l := &model.License{ValidFrom: anchor, ValidUntil: &now, Status: model.StatusCanceled}
	if aiUsable(l, now) || !aiUsable(l, anchor.Add(time.Hour)) {
		t.Fatal("paid cancellation/exact expiry boundary")
	}
	l.Status = model.StatusSuspended
	if aiUsable(l, anchor.Add(time.Hour)) {
		t.Fatal("suspended license usable")
	}
	china, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	localAnchor := time.Date(2025, 1, 31, 10, 0, 0, 0, china)
	_, boundary, err := MonthCycle(localAnchor.UTC(), time.Date(2025, 2, 28, 10, 0, 0, 0, china), "Asia/Shanghai")
	if err != nil || !boundary.Equal(time.Date(2025, 3, 31, 10, 0, 0, 0, china)) {
		t.Fatal("billing timezone or original anchor drifted")
	}
}

func TestAIProviderErrorsAndIncompleteSearch(t *testing.T) {
	for _, code := range []string{"insufficient_quota", "API_KEY_DISABLED", "API_KEY_EXPIRED", "unknown-with-secret"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(429)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": "SYNTHETIC-PRIVATE-BODY"})
		}))
		client := &JijiClient{BaseURL: server.URL, HTTP: server.Client()}
		_, err := client.Get(context.Background(), "SYNTHETIC-TOKEN", 1)
		server.Close()
		var pe *AIProviderError
		if !errors.As(err, &pe) || strings.Contains(err.Error(), "SYNTHETIC") || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe business error")
		}
		if code == "insufficient_quota" && pe.Code != code {
			t.Fatal("expected quota business state lost")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"code":0,"data":{}}`)) }))
	defer server.Close()
	client := &JijiClient{BaseURL: server.URL, HTTP: server.Client()}
	if _, err := client.Find(context.Background(), "synthetic", "name"); err == nil {
		t.Fatal("incomplete search treated as absent key")
	}
}

type fakeAI struct {
	mu                                sync.Mutex
	keys                              map[int64]*JijiKey
	creates, resets, rates, refreshes int
	failReset                         bool
	unknownCreate                     bool
	unknownRefresh                    bool
}

func (f *fakeAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Cookie") != "" {
		panic("cookie in independent client")
	}
	var body map[string]any
	if r.Method != "GET" {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	var data any
	if r.URL.Path == "/api/v1/auth/refresh" {
		f.refreshes++
		if f.unknownRefresh {
			w.WriteHeader(503)
			return
		}
		data = map[string]any{"access_token": "rotated-access", "refresh_token": "rotated-refresh", "expires_in": 86400}
	} else if r.URL.Path == "/api/v1/keys" {
		if r.Method == "GET" {
			items := []*JijiKey{}
			for _, k := range f.keys {
				items = append(items, k)
			}
			data = map[string]any{"items": items, "pages": 1}
		} else {
			f.creates++
			id := int64(f.creates)
			k := &JijiKey{ID: id, Key: fmt.Sprintf("synthetic-%d", id), Name: body["name"].(string), GroupID: int64(body["group_id"].(float64)), Quota: body["quota"].(float64), Status: "active"}
			f.keys[id] = k
			data = k
			if f.unknownCreate {
				w.WriteHeader(503)
				return
			}
		}
	} else {
		var id int64
		_, _ = fmt.Sscanf(r.URL.Path, "/api/v1/keys/%d", &id)
		k := f.keys[id]
		if k == nil {
			w.WriteHeader(404)
			return
		}
		if r.Method == "PUT" {
			if v, ok := body["status"].(string); ok {
				k.Status = v
			}
			if v, ok := body["quota"].(float64); ok {
				k.Quota = v
			}
			if v, ok := body["group_id"].(float64); ok {
				k.GroupID = int64(v)
			}
			if v, ok := body["expires_at"].(string); ok {
				x, _ := time.Parse(time.RFC3339, v)
				k.ExpiresAt = &x
			}
			if v, ok := body["rate_limit_5h"].(float64); ok {
				k.Rate5H = v
			}
			if v, ok := body["rate_limit_1d"].(float64); ok {
				k.Rate1D = v
			}
			if v, ok := body["rate_limit_7d"].(float64); ok {
				k.Rate7D = v
			}
			if body["reset_quota"] == true {
				f.resets++
				k.QuotaUsed = 0
				if f.failReset {
					w.WriteHeader(503)
					return
				}
			}
			if body["reset_rate_limit_usage"] == true {
				f.rates++
				k.Usage5H = 0
				k.Usage1D = 0
				k.Usage7D = 0
			}
		}
		data = k
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
}

func TestAIProvisioningIntegration(t *testing.T) {
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
	if err = s.RunMigrations("../../db/migrations"); err != nil {
		t.Fatal(err)
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	suffix := store.NewID()
	p := &model.Product{Name: "Synthetic AI", Slug: "movaura-studio", Type: "desktop"}
	if s.DB.NewSelect().Model(p).Where("slug = 'movaura-studio'").Scan(ctx) != nil {
		check(s.CreateProduct(ctx, p))
	}
	plan := &model.Plan{ProductID: p.ID, Name: "Synthetic", Slug: suffix, LicenseType: "subscription", BillingInterval: "year", LicenseModel: "standard", GraceDays: 0}
	check(s.CreatePlan(ctx, plan))
	profile := `{"provider_id":"jiji","base_url":"https://api-slb.jiji.cc/v1","protocol":"openai"}`
	ent := &model.Entitlement{PlanID: plan.ID, Feature: "studio.ai_profile", ValueType: "string", Value: profile}
	check(s.CreateEntitlement(ctx, ent))
	plan, err = s.FindPlanByID(ctx, plan.ID)
	check(err)
	binding, err := AIProfileBinding(plan)
	check(err)
	makeLicense := func() *model.License {
		end := time.Now().AddDate(1, 0, 0).Truncate(time.Second)
		l := &model.License{ProductID: p.ID, PlanID: plan.ID, Email: "synthetic@example.test", LicenseKey: store.NewID(), Status: model.StatusActive, ValidFrom: time.Now().Add(-time.Hour), ValidUntil: &end}
		check(s.CreateLicense(ctx, l))
		return l
	}
	legacy := makeLicense()
	cfg := model.AIPlanSettings{Enabled: true, GroupID: 53, Quota: .01, Rate5H: .02, Rate1D: .03, Rate7D: .04}
	check(s.SaveAIPlan(ctx, plan.ID, &store.AIPlanRecord{Config: cfg, ProfileBinding: binding}))
	f := &fakeAI{keys: map[int64]*JijiKey{}}
	server := httptest.NewServer(f)
	defer server.Close()
	aead, err := crypto.NewAESGCM(make([]byte, 32))
	check(err)
	ai := &AIService{Store: s, AEAD: aead, Client: &JijiClient{BaseURL: server.URL, HTTP: server.Client()}, Email: &EmailService{enabled: true}}
	check(ai.SaveAccount(ctx, p.ID, true, &AIAccountTokens{"synthetic-access", "synthetic-refresh", time.Now().Add(time.Hour).Unix()}))
	lic := makeLicense()
	if ok, e := s.AIDeliverable(ctx, lic.ID); e != nil || ok {
		t.Fatal("unprepared license deliverable")
	}
	var initialWorkers sync.WaitGroup
	for i := 0; i < 2; i++ {
		initialWorkers.Add(1)
		go func() { defer initialWorkers.Done(); ai.Tick(ctx) }()
	}
	initialWorkers.Wait()
	b, err := s.AIBinding(ctx, lic.ID)
	check(err)
	if b.State != "synced" || b.Revision != 1 || b.RemoteID != 1 || f.creates != 1 {
		t.Fatalf("issue state %s, revision %d, id %d, creates %d: %s", b.State, b.Revision, b.RemoteID, f.creates, b.LastError)
	}
	if _, err = s.AIBinding(ctx, legacy.ID); err == nil {
		t.Fatal("legacy backfilled")
	}
	var mailCount int
	check(s.DB.NewRaw("SELECT count(*) FROM email_queue WHERE id=?", "ai-license-issued:"+lic.ID).Scan(ctx, &mailCount))
	if mailCount != 1 {
		t.Fatal("prepared license delivery not queued exactly once")
	}
	lic.Plan = plan
	credential, err := ai.Credential(ctx, lic)
	check(err)
	if credential.APIKey != "synthetic-1" {
		t.Fatal("signed credential missing")
	}
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), "synthetic-1") {
		t.Fatal("key leaked")
	}
	var plain string
	check(ai.open(b.Secret, "key:"+lic.ID, &plain))
	if ai.open(b.Secret, "key:other", &plain) == nil {
		t.Fatal("AAD not bound")
	}
	// A later default change must not mutate the license snapshot.
	cfg.Quota = .02
	check(s.SaveAIPlan(ctx, plan.ID, &store.AIPlanRecord{Config: cfg, ProfileBinding: binding}))
	b, err = s.AIBinding(ctx, lic.ID)
	check(err)
	if b.Config.Quota != .01 {
		t.Fatal("retroactive defaults")
	}
	enqueue := func(action, dedupe string) { check(store.EnqueueAI(ctx, s.DB, lic.ID, action, dedupe)); ai.Tick(ctx) }
	f.keys[1].QuotaUsed = .005
	f.keys[1].Usage5H = .001
	enqueue("reset_quota", "manual-total")
	if f.resets != 1 || f.keys[1].Usage5H != .001 || f.keys[1].QuotaUsed != 0 {
		t.Fatal("total reset isolation")
	}
	f.keys[1].QuotaUsed = .002
	enqueue("reset_rate", "manual-rate")
	if f.rates != 1 || f.keys[1].QuotaUsed != .002 || f.keys[1].Usage5H != 0 {
		t.Fatal("period reset isolation")
	}
	enqueue("reset_quota", "manual-total")
	if f.resets != 1 {
		t.Fatal("idempotency failed")
	}
	enqueue("rotate", "rotate-1")
	b, err = s.AIBinding(ctx, lic.ID)
	check(err)
	if b.Revision != 2 || b.RemoteID != 2 || f.keys[1].Status != "inactive" {
		t.Fatalf("rotation: %s", b.LastError)
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET state='running' WHERE license_id=? AND action='rotate'", lic.ID)
	check(err)
	ai.Tick(ctx)
	b, err = s.AIBinding(ctx, lic.ID)
	check(err)
	if b.Revision != 2 || f.creates != 2 {
		t.Fatal("published rotation replayed")
	}
	// Early renewal preserves consumption. Resume uses the current license state.
	f.keys[2].QuotaUsed = .003
	end := lic.ValidUntil.AddDate(0, 1, 0)
	_, err = s.DB.ExecContext(ctx, "UPDATE licenses SET valid_until=? WHERE id=?", end, lic.ID)
	check(err)
	ai.Tick(ctx)
	if f.keys[2].QuotaUsed != .003 || !f.keys[2].ExpiresAt.Equal(end) {
		t.Fatal("renewal reset usage or lost exact expiry")
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE licenses SET status='suspended' WHERE id=?", lic.ID)
	check(err)
	ai.Tick(ctx)
	if f.keys[2].Status != "inactive" {
		t.Fatal("suspend did not disable")
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE licenses SET status='active' WHERE id=?", lic.ID)
	check(err)
	ai.Tick(ctx)
	if f.keys[2].Status != "active" || f.keys[2].QuotaUsed != .003 {
		t.Fatal("resume reset consumption")
	}
	// Downtime spanning several months grants only the current period once.
	anchor := time.Now().AddDate(0, -4, 0)
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_bindings SET anchor=? WHERE license_id=?", anchor, lic.ID)
	check(err)
	prior := f.resets
	ai.Tick(ctx)
	ai.Tick(ctx)
	if f.resets != prior+1 {
		t.Fatal("catch-up issued multiple periods")
	}
	// Rewrap all AI secrets under a new purpose key without changing binding.
	nextAEAD, e := crypto.NewAESGCM([]byte(strings.Repeat("x", 32)))
	check(e)
	ai.PreviousAEAD = ai.AEAD
	ai.AEAD = nextAEAD
	check(ai.Reencrypt(ctx))
	ai.PreviousAEAD = nil
	credential, e = ai.Credential(ctx, lic)
	check(e)
	if credential.APIKey != "synthetic-2" {
		t.Fatal("key rotation lost credential")
	}
	f.failReset = true
	enqueue("reset_quota", "unknown-reset")
	before := f.resets
	enqueue("reset_quota", "blocked-reset")
	if f.resets != before {
		t.Fatal("uncertain reset replay")
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET state='running' WHERE license_id=? AND phase='reset_sent'", lic.ID)
	check(err)
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_bindings SET anchor=? WHERE license_id=?", time.Now().AddDate(0, -6, 0), lic.ID)
	check(err)
	ai.Tick(ctx)
	var unknownCount int
	check(s.DB.NewRaw("SELECT count(*) FROM license_ai_jobs WHERE license_id=? AND phase='reset_sent' AND state='uncertain'", lic.ID).Scan(ctx, &unknownCount))
	if unknownCount != 1 || f.resets != before {
		t.Fatal("cross-period crash forgot ambiguous reset")
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE licenses SET status='revoked' WHERE id=?", lic.ID)
	check(err)
	ai.Tick(ctx)
	if f.keys[2].Status != "inactive" {
		t.Fatal("uncertainty blocked revocation")
	}
	// Ambiguous create recovers by its deterministic name, never by POST replay.
	f.failReset = false
	f.unknownCreate = true
	other := makeLicense()
	ai.Tick(ctx)
	posted := f.creates
	jobs, e := s.AIJobs(ctx, other.ID)
	check(e)
	var pendingID string
	for _, j := range jobs {
		if j.Phase == "create_sent" {
			pendingID = j.ID
		}
	}
	if pendingID == "" {
		t.Fatal("uncertain create checkpoint missing")
	}
	f.unknownCreate = false
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET state='pending' WHERE id=?", pendingID)
	check(err)
	ai.Tick(ctx)
	ob, e := s.AIBinding(ctx, other.ID)
	check(e)
	if ob.RemoteID == 0 || f.creates != posted {
		t.Fatal("create recovery duplicated provider key")
	}
	// No delivery before a binding, and no enrollment of unrelated products.
	if ok, e := s.AIDeliverable(ctx, other.ID); e != nil || !ok {
		t.Fatal("prepared license not deliverable")
	}
	foreign := &model.Product{Name: "Other product", Slug: "other-" + suffix, Type: "desktop"}
	check(s.CreateProduct(ctx, foreign))
	foreignPlan := &model.Plan{ProductID: foreign.ID, Name: "Other", Slug: "other", LicenseType: "subscription", BillingInterval: "month", LicenseModel: "standard"}
	check(s.CreatePlan(ctx, foreignPlan))
	check(s.SaveAIPlan(ctx, foreignPlan.ID, &store.AIPlanRecord{Config: cfg, ProfileBinding: binding}))
	e = s.CreateLicense(ctx, &model.License{ProductID: foreign.ID, PlanID: foreignPlan.ID, Email: "synthetic@example.test", LicenseKey: store.NewID(), Status: model.StatusActive, ValidUntil: &end})
	if e == nil {
		t.Fatal("cross-product provisioning accepted")
	}
	// Account refresh is serialized, rotation persisted, and failure stops retries.
	check(ai.SaveAccount(ctx, p.ID, true, &AIAccountTokens{"old", "old-refresh", time.Now().Add(time.Minute).Unix()}))
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := ai.Account(ctx, p.ID)
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if f.refreshes != 1 {
		t.Fatal("refresh rotation raced")
	}
	f.unknownRefresh = true
	check(ai.SaveAccount(ctx, p.ID, true, &AIAccountTokens{"old", "old-refresh", time.Now().Add(time.Minute).Unix()}))
	_, err = ai.Account(ctx, p.ID)
	if err == nil {
		t.Fatal("unknown refresh accepted")
	}
	_, _ = ai.Account(ctx, p.ID)
	if f.refreshes != 2 {
		t.Fatal("unknown refresh retried")
	}
	// Transport errors never expose raw token-bearing bodies.
	_, err = ai.Client.Get(ctx, "synthetic-access", 999)
	var pe *AIProviderError
	if !errors.As(err, &pe) {
		t.Fatal("unsafe provider error")
	}
}
