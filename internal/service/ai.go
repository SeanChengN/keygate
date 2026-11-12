package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
	"github.com/uptrace/bun"
)

type AIService struct {
	Store        *store.Store
	AEAD         *crypto.AESGCM
	PreviousAEAD *crypto.AESGCM
	Client       *JijiClient
	Email        *EmailService
}

func (s *AIService) seal(v any, aad string) ([]byte, error) {
	if s.AEAD == nil {
		return nil, errors.New("AI encryption is not configured")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return s.AEAD.Encrypt(raw, []byte(aad))
}
func (s *AIService) open(raw []byte, aad string, v any) error {
	if s.AEAD == nil {
		return errors.New("AI encryption is not configured")
	}
	plain, err := s.AEAD.Decrypt(raw, []byte(aad))
	if err != nil && s.PreviousAEAD != nil {
		plain, err = s.PreviousAEAD.Decrypt(raw, []byte(aad))
	}
	if err != nil {
		return errors.New("AI credential cannot be decrypted")
	}
	defer clear(plain)
	if json.Unmarshal(plain, v) != nil {
		return errors.New("AI credential is invalid")
	}
	return nil
}

func ValidateAIConfig(c model.AIPlanSettings) error {
	if !c.Enabled && c.GroupID == 0 {
		c.GroupID = 1 // An unconfigured disabled plan still validates monetary values.
	}
	return c.ValidateLimits()
}

// AIProfileBinding matches Python's JSON array encoding in package_ai.binding.
func AIProfileBinding(p *model.Plan) (string, error) {
	if p == nil {
		return "", errors.New("AI requires a plan")
	}
	for _, e := range p.Entitlements {
		if e.Feature == "studio.ai_profile" {
			var v struct {
				ProviderID string `json:"provider_id"`
				BaseURL    string `json:"base_url"`
				Protocol   string `json:"protocol"`
			}
			if json.Unmarshal([]byte(e.Value), &v) != nil {
				return "", errors.New("a signed jiji studio.ai_profile is required")
			}
			u, parseErr := url.Parse(v.BaseURL)
			if parseErr != nil || v.ProviderID != "jiji" || v.Protocol != "openai" || u.Scheme != "https" || u.Host != "api-slb.jiji.cc" || strings.TrimRight(u.Path, "/") != "/v1" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
				return "", errors.New("a signed jiji studio.ai_profile is required")
			}
			parts := []string{}
			for _, part := range []string{v.ProviderID, strings.TrimRight(v.BaseURL, "/"), v.Protocol} {
				raw, _ := json.Marshal(part)
				parts = append(parts, string(raw))
			}
			sum := sha256.Sum256([]byte("[" + strings.Join(parts, ", ") + "]"))
			return hex.EncodeToString(sum[:]), nil
		}
	}
	return "", errors.New("studio.ai_profile is missing")
}

// MonthCycle preserves the original day rather than drifting after February.
func MonthCycle(anchor, now time.Time, zone string) (string, time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return "", time.Time{}, err
	}
	a := anchor.In(loc)
	n := now.In(loc)
	boundary := func(offset int) time.Time {
		first := time.Date(a.Year(), a.Month()+time.Month(offset), 1, a.Hour(), a.Minute(), a.Second(), a.Nanosecond(), loc)
		last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, loc).Day()
		day := a.Day()
		if day > last {
			day = last
		}
		return time.Date(first.Year(), first.Month(), day, a.Hour(), a.Minute(), a.Second(), a.Nanosecond(), loc)
	}
	index := (n.Year()-a.Year())*12 + int(n.Month()-a.Month())
	if index < 0 {
		index = 0
	}
	if boundary(index).After(n) && index > 0 {
		index--
	}
	return fmt.Sprint(index), boundary(index + 1), nil
}

func (s *AIService) Account(ctx context.Context, productID string) (*AIAccountTokens, error) {
	var tokens AIAccountTokens
	err := s.Store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "ai-account:"+productID); err != nil {
			return err
		}
		var row struct {
			Secret  []byte
			Enabled bool
			State   string
		}
		if err := s.Store.DB.NewSelect().Table("ai_provider_accounts").Column("secret", "enabled", "state").Where("product_id = ?", productID).Scan(ctx, &row); err != nil {
			return errors.New("jiji account is not configured")
		}
		if !row.Enabled || row.State != "ready" {
			return errors.New("jiji account needs administrator attention")
		}
		if err := s.open(row.Secret, "account:"+productID, &tokens); err != nil {
			return err
		}
		if tokens.ExpiresAt <= time.Now().Unix()+300 {
			// Persist BEFORE rotating the upstream refresh token. A crash or unknown
			// response must require intervention, never replay the previous token.
			if _, err := s.Store.DB.ExecContext(ctx, "UPDATE ai_provider_accounts SET state = 'refreshing' WHERE product_id = ?", productID); err != nil {
				return err
			}
			next, err := s.Client.Refresh(ctx, &tokens)
			if err != nil {
				_, _ = s.Store.DB.ExecContext(ctx, "UPDATE ai_provider_accounts SET state = 'attention' WHERE product_id = ?", productID)
				return nil
			}
			tokens = *next
			raw, err := s.seal(tokens, "account:"+productID)
			if err != nil {
				return err
			}
			_, err = s.Store.DB.ExecContext(ctx, "UPDATE ai_provider_accounts SET secret = ?, state = 'ready', updated_at = now() WHERE product_id = ?", raw, productID)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if tokens.ExpiresAt <= time.Now().Unix()+300 {
		return nil, errors.New("jiji refresh needs administrator attention")
	}
	return &tokens, nil
}

func (s *AIService) SaveAccount(ctx context.Context, id string, enabled bool, tokens *AIAccountTokens) error {
	if s.AEAD == nil {
		return errors.New("AI encryption is not configured")
	}
	return s.Store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "ai-account:"+id); err != nil {
			return err
		}
		if tokens == nil {
			result, err := tx.ExecContext(ctx, "UPDATE ai_provider_accounts SET enabled = ?, updated_at = now() WHERE product_id = ?", enabled, id)
			if err != nil {
				return err
			}
			if count, _ := result.RowsAffected(); count == 0 {
				return errors.New("jiji account is not configured")
			}
			return nil
		}
		if tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.ExpiresAt <= time.Now().Unix() {
			return errors.New("access token, refresh token and future expires_at are required")
		}
		raw, err := s.seal(tokens, "account:"+id)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_provider_accounts(product_id, enabled, secret) VALUES (?, ?, ?) ON CONFLICT (product_id) DO UPDATE SET enabled = EXCLUDED.enabled, secret = EXCLUDED.secret, credential_revision = ai_provider_accounts.credential_revision + 1, state = 'ready', updated_at = now()`, id, enabled, raw)
		return err
	})
}

func (s *AIService) Credential(ctx context.Context, lic *model.License) (*model.AICredential, error) {
	b, err := s.Store.AIBinding(ctx, lic.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if b.RemoteID == 0 || len(b.Secret) == 0 {
		return nil, nil
	}
	currentBinding, err := AIProfileBinding(lic.Plan)
	if err != nil || currentBinding != b.ProfileBinding {
		return nil, nil
	}
	var key string
	if err := s.open(b.Secret, "key:"+lic.ID, &key); err != nil {
		return nil, err
	}
	return &model.AICredential{Version: 1, ProviderID: "jiji", ProfileBinding: b.ProfileBinding, Revision: b.Revision, APIKey: key}, nil
}

func (s *AIService) Start(ctx context.Context) {
	s.Tick(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Tick(ctx)
		}
	}
}
func (s *AIService) Tick(ctx context.Context) {
	if s.AEAD == nil {
		return
	}
	s.processDeletions(ctx)
	// Scan only explicit bindings. Legacy licenses cannot be enrolled by this loop.
	var bindings []*model.AIBinding
	if s.Store.DB.NewSelect().Model(&bindings).Scan(ctx) != nil {
		return
	}
	for _, b := range bindings {
		if deleting, err := store.AIDeleting(ctx, s.Store.DB, b.LicenseID); err != nil || deleting {
			continue
		}
		lic, err := s.Store.FindLicenseByID(ctx, b.LicenseID)
		if err != nil {
			continue
		}
		if aiUsable(lic, time.Now()) && b.RemoteID == 0 {
			_ = store.EnqueueAI(ctx, s.Store.DB, b.LicenseID, "sync", "issue-ready")
		}
		if aiUsable(lic, time.Now()) && b.RemoteID > 0 {
			cycle, _, err := MonthCycle(b.Anchor, time.Now(), b.Timezone)
			if err == nil && cycle != "0" {
				_ = store.EnqueueAI(ctx, s.Store.DB, b.LicenseID, "monthly", "month:"+cycle)
			}
		}
	}
	var jobs []*model.AIJob
	if s.Store.DB.NewRaw(`SELECT j.* FROM license_ai_jobs j JOIN licenses l ON l.id = j.license_id
        WHERE l.status <> 'pending_activation' AND j.state IN ('pending','running') AND j.next_attempt_at <= now()
        AND (l.status IN ('revoked','suspended','expired') OR l.valid_until <= now() OR NOT EXISTS (
            SELECT 1 FROM license_ai_jobs blocker WHERE blocker.license_id = j.license_id AND blocker.id <> j.id
            AND (blocker.state = 'uncertain' OR blocker.state = 'failed' AND (blocker.action <> 'sync' OR blocker.phase NOT IN ('','published')))
        )) ORDER BY j.created_at, j.id LIMIT 50`).Scan(ctx, &jobs) != nil {
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		s.process(ctx, job)
	}
}

func aiUsable(l *model.License, now time.Time) bool {
	return !now.Before(l.ValidFrom) && l.ValidUntil != nil && now.Before(*l.ValidUntil) && (l.Status == model.StatusActive || l.Status == model.StatusTrialing || l.Status == model.StatusPastDue || l.Status == model.StatusCanceled)
}

func (s *AIService) process(ctx context.Context, job *model.AIJob) {
	// Hold the license row throughout one operation: lifecycle updates serialize
	// with reconciliation, preventing an old task from enabling a revoked key.
	_ = s.Store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var locked bool
		if err := tx.NewRaw("SELECT pg_try_advisory_xact_lock(hashtextextended(?, 0))", "ai-license:"+job.LicenseID).Scan(ctx, &locked); err != nil || !locked {
			return err
		}
		if deleting, err := store.AIDeleting(ctx, tx, job.LicenseID); err != nil {
			return err
		} else if deleting {
			return nil
		}
		lic := new(model.License)
		if err := tx.NewSelect().Model(lic).Where("id = ?", job.LicenseID).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		plan, err := s.Store.FindPlanByID(ctx, lic.PlanID)
		if err != nil {
			return err
		}
		lic.Plan = plan
		fresh := new(model.AIJob)
		if err := s.Store.DB.NewSelect().Model(fresh).Where("id = ?", job.ID).Scan(ctx); err != nil {
			return err
		}
		if fresh.State != "pending" && fresh.State != "running" {
			return nil
		}
		*job = *fresh
		b, err := s.Store.AIBinding(ctx, lic.ID)
		if err != nil {
			return err
		}
		// An ambiguous operation is a barrier. Revocation may still disable all
		// known keys; it must never enable an old key during an uncertain rotation.
		blocked, err := s.Store.DB.NewSelect().Model((*model.AIJob)(nil)).Where("license_id = ? AND (state = 'uncertain' OR state = 'failed' AND (action <> 'sync' OR phase NOT IN ('','published'))) AND id <> ?", lic.ID, job.ID).Exists(ctx)
		if err != nil {
			return err
		}
		if blocked && aiUsable(lic, time.Now()) {
			return nil
		}
		finish := func(state string, e error) {
			msg := ""
			if e != nil {
				msg = e.Error()
			}
			_, _ = s.Store.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET state = ?, last_error = ? WHERE id = ?", state, msg, job.ID)
			b.State = state
			if state == "done" {
				b.State = "synced"
				if blocked {
					b.State = "uncertain"
					msg = "disabled keys synchronized; an earlier operation still requires confirmation"
				}
			}
			b.LastError = msg
			_, _ = s.Store.DB.NewUpdate().Model(b).Column("state", "last_error", "remote_view", "updated_at").WherePK().Exec(ctx)
		}
		if !aiUsable(lic, time.Now()) && b.RemoteID == 0 && b.CandidateID == 0 && job.Phase == "" {
			finish("done", nil)
			return nil
		}
		tokens, err := s.Account(ctx, b.ProductID)
		if err != nil {
			finish("failed", err)
			return nil
		}
		// Keep unresolved safety barriers visible even when a later revocation
		// succeeds. A successful disable is not confirmation of an unknown reset.
		_, _ = s.Store.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET state = 'running', attempts = attempts + 1 WHERE id = ?", job.ID)
		err = s.execute(ctx, lic, b, job, tokens.AccessToken)
		if err != nil {
			state := "failed"
			var pe *AIProviderError
			if errors.As(err, &pe) {
				if pe.Uncertain {
					state = "uncertain"
				}
				if pe.Status == 401 || pe.Status == 403 || pe.Status == 429 && pe.Code != "insufficient_quota" {
					_, _ = s.Store.DB.ExecContext(ctx, "UPDATE ai_provider_accounts SET state = 'attention' WHERE product_id = ?", b.ProductID)
				}
			}
			if strings.Contains(err.Error(), "manual resolution") {
				state = "uncertain"
			}
			finish(state, err)
			if state == "failed" && errors.As(err, &pe) && (pe.Status == 0 || pe.Status >= 500) && !pe.Uncertain && job.Attempts < 2 {
				_, _ = s.Store.DB.ExecContext(ctx, "UPDATE license_ai_jobs SET state = 'pending', next_attempt_at = ? WHERE id = ?", time.Now().Add(time.Duration(job.Attempts+1)*time.Minute), job.ID)
			}
		} else {
			finish("done", nil)
		}
		return nil
	})
}

func (s *AIService) checkpoint(ctx context.Context, b *model.AIBinding, j *model.AIJob, phase string) error {
	b.UpdatedAt = time.Now()
	err := s.Store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewUpdate().Model(b).WherePK().Exec(ctx); err != nil {
			return err
		}
		if phase == "published" {
			cycle, _, err := MonthCycle(b.Anchor, time.Now(), b.Timezone)
			if err != nil {
				return err
			}
			if cycle != "0" {
				if err := store.EnqueueAI(ctx, tx, b.LicenseID, "monthly", "month:"+cycle); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, "UPDATE license_ai_jobs SET state='done' WHERE license_id=? AND dedupe_key=? AND state='pending'", b.LicenseID, "month:"+cycle); err != nil {
					return err
				}
			}
			if b.Revision == 1 && b.Origin == "issuance" && s.Email != nil && s.Email.IsConfigured() {
				lic, err := s.Store.FindLicenseByID(ctx, b.LicenseID)
				if err != nil {
					return err
				}
				product, err := s.Store.FindProductByID(ctx, b.ProductID)
				if err != nil {
					return err
				}
				key := s.Store.DecryptLicenseKey(lic)
				if key == "" {
					return errors.New("license delivery key unavailable")
				}
				body := renderTemplate(s.Email.getTemplate("license_created", tmplLicenseCreated), map[string]string{"Product": product.Name, "Plan": lic.Plan.Name, "LicenseKey": key})
				if _, err := tx.ExecContext(ctx, "INSERT INTO email_queue(id,to_addr,subject,body,max_attempts) VALUES(?,?,?,?,5) ON CONFLICT(id) DO NOTHING", "ai-license-issued:"+lic.ID, lic.Email, "Your license for "+product.Name, body); err != nil {
					return err
				}
			}
		}
		_, err := tx.ExecContext(ctx, "UPDATE license_ai_jobs SET phase = ? WHERE id = ?", phase, j.ID)
		return err
	})
	if err == nil {
		j.Phase = phase
	}
	return err
}

func (s *AIService) execute(ctx context.Context, l *model.License, b *model.AIBinding, j *model.AIJob, token string) error {
	usable := aiUsable(l, time.Now())
	if !usable {
		unknownCreate, err := s.Store.DB.NewSelect().Model((*model.AIJob)(nil)).Where("license_id = ? AND phase = 'create_sent' AND state IN ('failed','uncertain','running')", l.ID).Exists(ctx)
		if err != nil {
			return err
		}
		for _, id := range []int64{b.RemoteID, b.CandidateID} {
			if id > 0 {
				if err := s.syncRemote(ctx, token, id, l, b, false); err != nil {
					return err
				}
			}
		}
		if j.Phase == "reset_sent" {
			return errors.New("reset result unknown; manual resolution required")
		}
		// Recover an unrecorded create before declaring a revocation synchronized.
		if b.CandidateID == 0 && (j.Phase == "create_sent" || unknownCreate) {
			key, err := s.Client.Find(ctx, token, fmt.Sprintf("keygate-%s-g%d", l.ID, b.Generation))
			if err != nil {
				return err
			}
			if key == nil {
				return errors.New("create result unknown; manual resolution required")
			}
			b.CandidateID = key.ID
			b.CandidateSecret, err = s.seal(key.Key, "key:"+l.ID)
			if err != nil {
				return err
			}
			if err := s.checkpoint(ctx, b, j, "candidate_disabled_pending"); err != nil {
				return err
			}
			return s.syncRemote(ctx, token, key.ID, l, b, false)
		}
		if j.Action == "rotate" && j.Phase != "" && j.Phase != "published" {
			return errors.New("rotation paused by license state; retry after reviewing license validity")
		}
		return nil
	}
	if j.Phase == "reset_sent" {
		return errors.New("reset result unknown; manual resolution required")
	}
	if b.RemoteID == 0 || j.Action == "rotate" && j.Phase != "published" {
		if j.Action == "rotate" && j.Phase == "" {
			b.Generation++
			if err := s.checkpoint(ctx, b, j, "rotation_started"); err != nil {
				return err
			}
		}
		name := fmt.Sprintf("keygate-%s-g%d", l.ID, b.Generation)
		if b.CandidateID == 0 {
			key, err := s.Client.Find(ctx, token, name)
			if err != nil {
				return err
			}
			if key == nil {
				if j.Phase == "create_sent" {
					return errors.New("create result unknown; manual resolution required")
				}
				if err := s.checkpoint(ctx, b, j, "create_sent"); err != nil {
					return err
				}
				key, err = s.Client.Create(ctx, token, name, b.Config)
				if err != nil {
					return err
				}
			}
			if key.ID <= 0 || key.Key == "" || key.GroupID != b.Config.GroupID {
				return errors.New("provider identity mismatch; manual resolution required")
			}
			raw, err := s.seal(key.Key, "key:"+l.ID)
			if err != nil {
				return err
			}
			b.CandidateID = key.ID
			b.CandidateSecret = raw
			if err := s.checkpoint(ctx, b, j, "candidate_saved"); err != nil {
				return err
			}
		}
		if err := s.syncRemote(ctx, token, b.CandidateID, l, b, usable); err != nil {
			return err
		}
		if b.RemoteID > 0 && b.RemoteID != b.CandidateID {
			if err := s.Client.Update(ctx, token, b.RemoteID, map[string]any{"status": "inactive"}); err != nil {
				return err
			}
			old, err := s.Client.Get(ctx, token, b.RemoteID)
			if err != nil {
				return err
			}
			if old.Status != "inactive" {
				return errors.New("old key disable readback failed")
			}
		}
		b.RemoteID = b.CandidateID
		b.Secret = b.CandidateSecret
		b.CandidateID = 0
		b.CandidateSecret = nil
		b.Revision++
		if err := s.checkpoint(ctx, b, j, "published"); err != nil {
			return err
		}
	}
	if err := s.syncRemote(ctx, token, b.RemoteID, l, b, usable); err != nil {
		return err
	}
	if j.Action == "reset_quota" || j.Action == "reset_rate" || j.Action == "monthly" {
		if !usable {
			return nil
		}
		if j.Action == "monthly" {
			cycle, _, err := MonthCycle(b.Anchor, time.Now(), b.Timezone)
			if err != nil {
				return err
			}
			if j.DedupeKey != "month:"+cycle {
				return nil
			}
		}
		if j.Phase != "reset_done" {
			before, err := s.Client.Get(ctx, token, b.RemoteID)
			if err != nil {
				return err
			}
			b.RemoteView = before.Public()
			if err := s.checkpoint(ctx, b, j, "reset_sent"); err != nil {
				return err
			}
			field := "reset_quota"
			if j.Action == "reset_rate" {
				field = "reset_rate_limit_usage"
			}
			if err := s.Client.Update(ctx, token, b.RemoteID, map[string]any{field: true}); err != nil {
				return err
			}
			after, err := s.Client.Get(ctx, token, b.RemoteID)
			if err != nil {
				return errors.New("reset readback unknown; manual resolution required")
			}
			if after.Quota != before.Quota || after.GroupID != before.GroupID || after.Status != before.Status || after.ExpiresAt == nil || before.ExpiresAt == nil || !after.ExpiresAt.Equal(*before.ExpiresAt) || after.Rate5H != before.Rate5H || after.Rate1D != before.Rate1D || after.Rate7D != before.Rate7D ||
				field == "reset_quota" && (after.QuotaUsed != 0 || after.Usage5H != before.Usage5H || after.Usage1D != before.Usage1D || after.Usage7D != before.Usage7D) ||
				field == "reset_rate_limit_usage" && (after.QuotaUsed != before.QuotaUsed || after.Usage5H != 0 || after.Usage1D != 0 || after.Usage7D != 0) {
				return errors.New("reset readback mismatch; manual resolution required")
			}
			if err := s.checkpoint(ctx, b, j, "reset_done"); err != nil {
				return err
			}
		}
	}
	key, err := s.Client.Get(ctx, token, b.RemoteID)
	if err != nil {
		return err
	}
	b.RemoteView = key.Public()
	return nil
}

func (s *AIService) syncRemote(ctx context.Context, token string, id int64, l *model.License, b *model.AIBinding, usable bool) error {
	if id == 0 {
		return nil
	}
	before, err := s.Client.Get(ctx, token, id)
	if err != nil {
		return err
	}
	if usable {
		profile, err := AIProfileBinding(l.Plan)
		if err != nil || profile != b.ProfileBinding {
			return errors.New("AI plan route changed; administrator action required")
		}
	}
	status := "inactive"
	if usable {
		status = "active"
	}
	v := map[string]any{"status": status, "quota": b.Config.Quota, "group_id": b.Config.GroupID, "rate_limit_5h": b.Config.Rate5H, "rate_limit_1d": b.Config.Rate1D, "rate_limit_7d": b.Config.Rate7D}
	if l.ValidUntil != nil {
		v["expires_at"] = l.ValidUntil.UTC().Truncate(time.Second).Format(time.RFC3339)
	}
	if err := s.Client.Update(ctx, token, id, v); err != nil {
		return err
	}
	k, err := s.Client.Get(ctx, token, id)
	if err != nil {
		return err
	}
	if before.Key != k.Key || k.QuotaUsed < before.QuotaUsed {
		return errors.New("provider changed key or cleared historical usage; manual resolution required")
	}
	if k.ID != id || k.Status != status || k.GroupID != b.Config.GroupID || k.Quota != b.Config.Quota || k.Rate5H != b.Config.Rate5H || k.Rate1D != b.Config.Rate1D || k.Rate7D != b.Config.Rate7D || l.ValidUntil != nil && (k.ExpiresAt == nil || k.ExpiresAt.Unix() != l.ValidUntil.Unix()) {
		return errors.New("provider configuration readback mismatch")
	}
	if id == b.RemoteID {
		b.RemoteView = k.Public()
	}
	return nil
}
