package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAIKeyAbsenceIsSpecific(t *testing.T) {
	for _, tc := range []struct {
		body    string
		missing bool
	}{{`{"code":404,"message":"api key not found"}`, true}, {`{"code":404,"message":"route not found"}`, false}, {`<html>404</html>`, false}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404); fmt.Fprint(w, tc.body) }))
		c := &JijiClient{BaseURL: server.URL, HTTP: server.Client()}
		_, err := c.Get(context.Background(), "synthetic", 1)
		server.Close()
		if IsJijiKeyAbsent(err) != tc.missing {
			t.Fatal("generic 404 accepted as key absence")
		}
	}
}

func TestAIDeletionRecoveryAndIsolation(t *testing.T) {
	dsn := os.Getenv("AI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated database required")
	}
	s, err := store.New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(s.RunMigrations("../../db/migrations"))
	p := &model.Product{Name: "Delete synthetic", Slug: "movaura-studio", Type: "desktop"}
	if s.DB.NewSelect().Model(p).Where("slug='movaura-studio'").Scan(ctx) != nil {
		check(s.CreateProduct(ctx, p))
	}
	plan := &model.Plan{ProductID: p.ID, Name: "Delete synthetic", Slug: store.NewID(), LicenseType: "subscription", BillingInterval: "month", GraceDays: 0, Active: true, LicenseModel: "standard"}
	check(s.CreatePlan(ctx, plan))
	aead, err := crypto.NewAESGCM(make([]byte, 32))
	check(err)
	keys := map[int64]*JijiKey{}
	deletes := map[int64]int{}
	timeoutID := int64(0)
	rejectID := int64(0)
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/v1/keys/"), 10, 64)
		k := keys[id]
		if r.Method != "GET" {
			writes++
		}
		if k == nil {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"code":404,"message":"api key not found"}`)
			return
		}
		if r.Method == "PUT" {
			k.Status = "inactive"
		}
		if r.Method == "DELETE" {
			deletes[id]++
			if id == rejectID {
				w.WriteHeader(429)
				fmt.Fprint(w, `{"code":"rate_limit"}`)
				return
			}
			delete(keys, id)
			if id == timeoutID {
				w.WriteHeader(503)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": k})
	}))
	defer server.Close()
	ai := &AIService{Store: s, AEAD: aead, Client: &JijiClient{BaseURL: server.URL, HTTP: server.Client()}}
	check(ai.SaveAccount(ctx, p.ID, true, &AIAccountTokens{"synthetic", "synthetic-refresh", time.Now().Add(time.Hour).Unix()}))
	next := int64(1000)
	makeLicense := func(status string, candidate, duplicate bool) *model.License {
		next += 10
		end := time.Now().Add(time.Hour)
		l := &model.License{ProductID: p.ID, PlanID: plan.ID, LicenseKey: store.NewID(), Status: status, ValidFrom: time.Now(), ValidUntil: &end}
		check(s.CreateLicense(ctx, l))
		k := &JijiKey{ID: next, Key: fmt.Sprint("synthetic-key-", next), Status: "active"}
		keys[next] = k
		secret, e := ai.seal(k.Key, "key:"+l.ID)
		check(e)
		b := &model.AIBinding{LicenseID: l.ID, ProductID: p.ID, RemoteID: next, Secret: secret, Revision: 1, State: "synced", Anchor: time.Now(), Timezone: "UTC", RemoteView: map[string]any{"status": "inactive"}, UpdatedAt: time.Now()}
		if candidate {
			cid := next + 1
			if duplicate {
				cid = next
			}
			ck := &JijiKey{ID: cid, Key: fmt.Sprint("synthetic-key-", cid), Status: "active"}
			keys[cid] = ck
			b.CandidateID = cid
			b.CandidateSecret, e = ai.seal(ck.Key, "key:"+l.ID)
			check(e)
		}
		_, e = s.DB.NewInsert().Model(b).Exec(ctx)
		check(e)
		return l
	}
	// Payment and status are validated before the first upstream write.
	active := makeLicense(model.StatusActive, false, false)
	if _, err = ai.RequestDeletion(ctx, active.ID, "admin", ""); !errors.Is(err, store.ErrLicenseNotRevoked) {
		t.Fatal("active license accepted")
	}
	check(s.RevokeLicense(ctx, active.ID))
	_, err = s.DB.ExecContext(ctx, "UPDATE licenses SET payment_provider='synthetic' WHERE id=?", active.ID)
	check(err)
	if _, err = ai.RequestDeletion(ctx, active.ID, "admin", ""); !errors.Is(err, store.ErrLicensePaymentLinked) {
		t.Fatal("payment linked license accepted")
	}
	if writes != 0 {
		t.Fatal("unsafe target caused provider write")
	}
	// Current/candidate delete survives an ambiguous successful DELETE; old history stays.
	l := makeLicense(model.StatusRevoked, true, false)
	b, err := s.AIBinding(ctx, l.ID)
	check(err)
	historical := next + 2
	keys[historical] = &JijiKey{ID: historical, Key: "historical", Status: "inactive"}
	timeoutID = b.RemoteID
	task, err := ai.RequestDeletion(ctx, l.ID, "admin", "")
	check(err)
	again, err := ai.RequestDeletion(ctx, l.ID, "admin", "")
	check(err)
	if task.ID != again.ID {
		t.Fatal("duplicate task")
	}
	if err = store.EnqueueAI(ctx, s.DB, l.ID, "sync", "should-block"); !errors.Is(err, store.ErrAIDeletionInProgress) {
		t.Fatal("delete barrier bypassed")
	}
	if _, err = s.DB.ExecContext(ctx, "UPDATE licenses SET status='active' WHERE id=?", l.ID); err == nil {
		t.Fatal("lifecycle bypassed delete barrier")
	}
	ai.processDeletion(ctx, l.ID)
	if _, err = s.FindLicenseByID(ctx, l.ID); err == nil {
		t.Fatal("local deletion did not finish")
	}
	done, err := s.AIDeletion(ctx, l.ID)
	check(err)
	if done.State != "done" || deletes[b.RemoteID] != 1 || deletes[b.CandidateID] != 1 || keys[historical] == nil {
		t.Fatal("wrong target deletion")
	}
	if _, err = s.DeleteRevokedUnpaidLicense(ctx, l.ID, "admin", ""); !errors.Is(err, store.ErrLicenseNotFound) {
		t.Fatal("deleted record reappeared")
	}
	// A restart after send confirms absence without replaying DELETE.
	resumed := makeLicense(model.StatusRevoked, false, false)
	rb, err := s.AIBinding(ctx, resumed.ID)
	check(err)
	task, err = ai.RequestDeletion(ctx, resumed.ID, "admin", "")
	check(err)
	task.Targets[0].Verified = true
	task.Targets[0].Phase = "delete_sent"
	task.State = "running"
	check(s.SaveAIDeletion(ctx, task))
	delete(keys, rb.RemoteID)
	restarted := &AIService{Store: s, AEAD: aead, Client: ai.Client}
	restarted.processDeletion(ctx, resumed.ID)
	done, err = s.AIDeletion(ctx, resumed.ID)
	check(err)
	if done.State != "done" || deletes[rb.RemoteID] != 0 {
		t.Fatal("restart replayed delete")
	}
	// Wrong credentials and unverified absence never delete local state.
	wrong := makeLicense(model.StatusRevoked, false, false)
	wb, err := s.AIBinding(ctx, wrong.ID)
	check(err)
	_, err = ai.RequestDeletion(ctx, wrong.ID, "admin", "")
	check(err)
	keys[wb.RemoteID].Key = "foreign-key"
	ai.processDeletion(ctx, wrong.ID)
	done, err = s.AIDeletion(ctx, wrong.ID)
	check(err)
	if done.State != "uncertain" || deletes[wb.RemoteID] != 0 {
		t.Fatal("foreign key deleted")
	}
	// A partial batch stops at 429; explicit retry continues without repeating completed target.
	partial := makeLicense(model.StatusRevoked, true, false)
	pb, err := s.AIBinding(ctx, partial.ID)
	check(err)
	_, err = ai.RequestDeletion(ctx, partial.ID, "admin", "")
	check(err)
	rejectID = pb.CandidateID
	ai.processDeletion(ctx, partial.ID)
	done, err = s.AIDeletion(ctx, partial.ID)
	check(err)
	if done.State != "failed" || done.Targets[0].Phase != "absent" {
		t.Fatal("partial failure lost progress")
	}
	rejectID = 0
	check(ai.SaveAccount(ctx, p.ID, true, &AIAccountTokens{"new", "new-refresh", time.Now().Add(time.Hour).Unix()}))
	_, err = ai.RequestDeletion(ctx, partial.ID, "admin", "")
	check(err)
	restarted.processDeletion(ctx, partial.ID)
	done, err = s.AIDeletion(ctx, partial.ID)
	check(err)
	if done.State != "done" || deletes[pb.RemoteID] != 1 {
		t.Fatal("retry repeated completed deletion")
	}
	// Missing records must have been identity-verified before accepting absence.
	missing := makeLicense(model.StatusRevoked, false, false)
	mb, e := s.AIBinding(ctx, missing.ID)
	check(e)
	_, err = ai.RequestDeletion(ctx, missing.ID, "admin", "")
	check(err)
	delete(keys, mb.RemoteID)
	ai.processDeletion(ctx, missing.ID)
	md, e := s.AIDeletion(ctx, missing.ID)
	check(e)
	if md.State == "done" {
		t.Fatal("unverified missing key treated as deleted")
	}
	// Per-license locks reject a concurrent request without provider writes.
	locked := makeLicense(model.StatusRevoked, false, false)
	tx, e := s.DB.BeginTx(ctx, nil)
	check(e)
	check(store.LockAIDeletion(ctx, tx, locked.ID))
	beforeWrites := writes
	if _, e = ai.RequestDeletion(ctx, locked.ID, "admin", ""); !errors.Is(e, store.ErrAIDeleteBusy) {
		t.Fatal("lock conflict not reported")
	}
	check(tx.Rollback())
	if writes != beforeWrites {
		t.Fatal("busy request wrote upstream")
	}
	// An unrecorded upstream creation cannot be discarded as a keyless record.
	unknown := makeLicense(model.StatusRevoked, false, false)
	check(store.EnqueueAI(ctx, s.DB, unknown.ID, "sync", "unknown-create"))
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET phase='create_sent' WHERE license_id=?", unknown.ID)
	check(err)
	if _, e = ai.RequestDeletion(ctx, unknown.ID, "admin", ""); !errors.Is(e, store.ErrAIDeleteUnconfirmed) {
		t.Fatal("unknown creation accepted")
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET state='done' WHERE license_id=?", unknown.ID)
	check(err)
	// A restart at local finalization never needs provider credentials again.
	local := makeLicense(model.StatusRevoked, false, false)
	lb, e := s.AIBinding(ctx, local.ID)
	check(e)
	localTask, e := ai.RequestDeletion(ctx, local.ID, "admin", "")
	check(e)
	localTask.Targets[0].Verified = true
	localTask.Targets[0].Phase = "absent"
	localTask.State = "finalizing"
	delete(keys, lb.RemoteID)
	check(s.SaveAIDeletion(ctx, localTask))
	ai.processDeletion(ctx, local.ID)
	localTask, e = s.AIDeletion(ctx, local.ID)
	check(e)
	if localTask.State != "done" || deletes[lb.RemoteID] != 0 {
		t.Fatal("local finalization called provider")
	}
	// The same current/candidate ID is deleted exactly once.
	duplicate := makeLicense(model.StatusRevoked, true, true)
	db, err := s.AIBinding(ctx, duplicate.ID)
	check(err)
	task, err = ai.RequestDeletion(ctx, duplicate.ID, "admin", "")
	check(err)
	if len(task.Targets) != 1 {
		t.Fatal("duplicate targets retained")
	}
	ai.processDeletion(ctx, duplicate.ID)
	if deletes[db.RemoteID] != 1 {
		t.Fatal("duplicate delete")
	}
	// Prevent leaked executable fixtures from contaminating subsequent tests.
	_, err = s.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET state='done' WHERE license_id IN (?,?)", active.ID, wrong.ID)
	check(err)
}
