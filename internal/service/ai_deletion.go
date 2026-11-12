package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
	"github.com/uptrace/bun"
	"time"
)

func keyFingerprint(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// RequestDeletion records intent before any upstream write; repeats reuse it.
func (s *AIService) RequestDeletion(ctx context.Context, id, actor, ip string) (*model.AIDeletion, error) {
	var result *model.AIDeletion
	err := s.Store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := store.LockAIDeletion(ctx, tx, id); err != nil {
			return err
		}
		l, err := store.ValidateDeletion(ctx, tx, id)
		if err != nil {
			return err
		}
		var accountRevision int64
		// Manual credential replacement is distinct from automatic refresh.
		revisionErr := tx.NewRaw("SELECT credential_revision FROM ai_provider_accounts WHERE product_id=?", l.ProductID).Scan(ctx, &accountRevision)
		if revisionErr != nil && !errors.Is(revisionErr, sql.ErrNoRows) {
			return revisionErr
		}
		prior := new(model.AIDeletion)
		err = tx.NewSelect().Model(prior).Where("license_id=?", id).For("UPDATE").Scan(ctx)
		if err == nil {
			if prior.State == "failed" || prior.State == "uncertain" {
				// An explicit retry may resend only after the worker rechecks identity and existence.
				for i := range prior.Targets {
					if prior.Targets[i].Phase == "delete_sent" {
						prior.Targets[i].Phase = "retry_requested"
					}
				}
				if prior.AccountRevision != accountRevision {
					for i := range prior.Targets {
						if prior.Targets[i].Phase != "absent" {
							prior.Targets[i].Verified = false
						}
					}
					prior.AccountRevision = accountRevision
				}
				prior.State = "pending"
				prior.LastErrorCode = ""
				prior.UpdatedAt = time.Now()
				if _, err = tx.NewUpdate().Model(prior).Column("state", "targets", "account_revision", "last_error_code", "updated_at").WherePK().Exec(ctx); err != nil {
					return err
				}
			}
			result = prior
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		b := new(model.AIBinding)
		err = tx.NewSelect().Model(b).Where("license_id=?", id).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		unknown, err := tx.NewSelect().Model((*model.AIJob)(nil)).Where("license_id=? AND phase='create_sent' AND state <> 'done'", id).Exists(ctx)
		if err != nil {
			return err
		}
		if unknown {
			return store.ErrAIDeleteUnconfirmed
		}
		targets := []model.AIDeleteTarget{}
		for _, pair := range []struct {
			id     int64
			secret []byte
		}{{b.RemoteID, b.Secret}, {b.CandidateID, b.CandidateSecret}} {
			if pair.id == 0 {
				continue
			}
			var key string
			if err = s.open(pair.secret, "key:"+id, &key); err != nil {
				return err
			}
			if key == "" {
				return errors.New("AI credential is invalid")
			}
			fingerprint := keyFingerprint(key)
			if len(targets) > 0 && targets[0].ID == pair.id {
				if targets[0].Fingerprint != fingerprint {
					return errors.New("provider identity mismatch")
				}
				continue
			}
			targets = append(targets, model.AIDeleteTarget{ID: pair.id, Fingerprint: fingerprint, Phase: "pending"})
		}
		if len(targets) == 0 {
			if _, err = tx.ExecContext(ctx, "UPDATE license_ai_jobs SET state='done',last_error='' WHERE license_id=?", id); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "UPDATE license_ai_bindings SET state='synced' WHERE license_id=?", id)
			return err
		}
		now := time.Now()
		d := &model.AIDeletion{ID: store.NewID(), LicenseID: id, ProductID: l.ProductID, ActorID: actor, IPAddress: ip, State: "pending", AccountRevision: accountRevision, Targets: targets, CreatedAt: now, UpdatedAt: now}
		if _, err = tx.NewInsert().Model(d).Exec(ctx); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE license_ai_jobs SET state='done',last_error='' WHERE license_id=?", id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE license_ai_bindings SET state='deleting',updated_at=now() WHERE license_id=?", id); err != nil {
			return err
		}
		_, err = tx.NewInsert().Model(&model.AuditLog{ID: store.NewID(), Entity: "license", EntityID: id, Action: "deletion_requested", ActorType: "admin", ActorID: actor, IPAddress: ip, Changes: map[string]any{"target_count": len(targets)}}).Exec(ctx)
		result = d
		return err
	})
	return result, err
}

func (s *AIService) processDeletions(ctx context.Context) {
	var records []*model.AIDeletion
	if s.Store.DB.NewSelect().Model(&records).Where("state IN ('pending','running','finalizing')").Order("created_at").Limit(20).Scan(ctx) != nil {
		return
	}
	for _, d := range records {
		if ctx.Err() != nil {
			return
		}
		s.processDeletion(ctx, d.LicenseID)
	}
}
func (s *AIService) processDeletion(ctx context.Context, id string) {
	finalize := false
	_ = s.Store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := store.LockAIDeletion(ctx, tx, id); err != nil {
			return err
		}
		if _, err := store.ValidateDeletion(ctx, tx, id); err != nil {
			return err
		}
		d, err := s.Store.AIDeletion(ctx, id)
		if err != nil {
			return err
		}
		if d.State != "pending" && d.State != "running" && d.State != "finalizing" {
			return nil
		}
		fail := func(err error) error {
			d.State = "failed"
			d.LastErrorCode = AIErrorCode(err)
			var pe *AIProviderError
			if errors.As(err, &pe) {
				if pe.Uncertain {
					d.State = "uncertain"
				}
				if pe.Status == 401 || pe.Status == 403 || pe.Status == 429 {
					_, _ = s.Store.DB.ExecContext(ctx, "UPDATE ai_provider_accounts SET state='attention' WHERE product_id=?", d.ProductID)
				}
			}
			if d.LastErrorCode == "AI_RESULT_UNCERTAIN" {
				d.State = "uncertain"
			}
			return s.Store.SaveAIDeletion(ctx, d)
		}
		allGone := true
		for _, t := range d.Targets {
			if t.Phase != "absent" {
				allGone = false
			}
		}
		if !allGone {
			var currentRevision int64
			if err := s.Store.DB.NewRaw("SELECT credential_revision FROM ai_provider_accounts WHERE product_id=?", d.ProductID).Scan(ctx, &currentRevision); err != nil {
				return fail(err)
			}
			if d.AccountRevision != currentRevision {
				return fail(errors.New("jiji account needs administrator attention"))
			}
			tokens, err := s.Account(ctx, d.ProductID)
			if err != nil {
				return fail(err)
			}
			d.State = "running"
			if err = s.Store.SaveAIDeletion(ctx, d); err != nil {
				return err
			}
			for i := range d.Targets {
				t := &d.Targets[i]
				if t.Phase == "absent" {
					continue
				}
				key, err := s.Client.Get(ctx, tokens.AccessToken, t.ID)
				if IsJijiKeyAbsent(err) && t.Verified {
					t.Phase = "absent"
					if err = s.Store.SaveAIDeletion(ctx, d); err != nil {
						return err
					}
					continue
				}
				if err != nil {
					return fail(err)
				}
				if key.ID != t.ID || keyFingerprint(key.Key) != t.Fingerprint {
					return fail(errors.New("provider identity mismatch; manual resolution required"))
				}
				t.Verified = true
				if err = s.Store.SaveAIDeletion(ctx, d); err != nil {
					return err
				}
				if key.Status != "inactive" {
					if err = s.Client.Update(ctx, tokens.AccessToken, t.ID, map[string]any{"status": "inactive"}); err != nil {
						return fail(err)
					}
					key, err = s.Client.Get(ctx, tokens.AccessToken, t.ID)
					if err != nil {
						return fail(err)
					}
					if key.ID != t.ID || keyFingerprint(key.Key) != t.Fingerprint || key.Status != "inactive" {
						return fail(errors.New("provider disable readback mismatch"))
					}
				}
				if t.Phase == "delete_sent" {
					return fail(errors.New("delete result unknown; manual resolution required"))
				}
				t.Phase = "delete_sent"
				if err = s.Store.SaveAIDeletion(ctx, d); err != nil {
					return err
				}
				deleteErr := s.Client.Delete(ctx, tokens.AccessToken, t.ID)
				if deleteErr != nil {
					var pe *AIProviderError
					if errors.As(deleteErr, &pe) && (pe.Status == 401 || pe.Status == 403 || pe.Status == 429) {
						return fail(deleteErr)
					}
				}
				_, readErr := s.Client.Get(ctx, tokens.AccessToken, t.ID)
				if !IsJijiKeyAbsent(readErr) {
					if deleteErr != nil {
						return fail(deleteErr)
					}
					return fail(errors.New("delete result unknown; manual resolution required"))
				}
				t.Phase = "absent"
				if err = s.Store.SaveAIDeletion(ctx, d); err != nil {
					return err
				}
			}
		}
		d.State = "finalizing"
		d.LastErrorCode = ""
		if err = s.Store.SaveAIDeletion(ctx, d); err != nil {
			return err
		}
		finalize = true
		return nil
	})
	if finalize {
		d, err := s.Store.AIDeletion(ctx, id)
		if err != nil {
			return
		}
		if _, err = s.Store.DeleteRevokedUnpaidLicense(ctx, id, d.ActorID, d.IPAddress); err != nil {
			if errors.Is(err, store.ErrAIDeleteBusy) {
				return
			}
			fresh, readErr := s.Store.AIDeletion(ctx, id)
			if readErr == nil && fresh.State == "done" {
				return
			}
			d.State = "failed"
			d.LastErrorCode = "AI_DELETE_LOCAL_FAILED"
			_ = s.Store.SaveAIDeletion(ctx, d)
		}
	}
}
