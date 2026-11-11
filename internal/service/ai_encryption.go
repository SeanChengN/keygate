package service

import (
	"context"
	"errors"

	"github.com/tabloy/keygate/internal/model"
	"github.com/uptrace/bun"
)

// Reencrypt uses the same account/license locks as the worker and administration
// paths. Current-key ciphertext stays byte-for-byte unchanged. No plaintext is logged.
func (s *AIService) Reencrypt(ctx context.Context) error {
	if s.AEAD == nil || s.PreviousAEAD == nil {
		return nil
	}
	rewrap := func(raw []byte, aad string) ([]byte, error) {
		if len(raw) == 0 {
			return raw, nil
		}
		plain, err := s.AEAD.Decrypt(raw, []byte(aad))
		if err == nil {
			clear(plain)
			return raw, nil
		}
		plain, err = s.PreviousAEAD.Decrypt(raw, []byte(aad))
		if err != nil {
			return nil, errors.New("AI credential rotation requires the correct previous master key")
		}
		defer clear(plain)
		return s.AEAD.Encrypt(plain, []byte(aad))
	}
	var ids []string
	if err := s.Store.DB.NewSelect().Table("ai_provider_accounts").Column("product_id").Scan(ctx, &ids); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.Store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "ai-account:"+id); err != nil {
				return err
			}
			var raw []byte
			if err := tx.NewRaw("SELECT secret FROM ai_provider_accounts WHERE product_id=?", id).Scan(ctx, &raw); err != nil {
				return err
			}
			next, err := rewrap(raw, "account:"+id)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "UPDATE ai_provider_accounts SET secret=? WHERE product_id=?", next, id)
			return err
		}); err != nil {
			return err
		}
	}
	ids = nil
	if err := s.Store.DB.NewSelect().Table("license_ai_bindings").Column("license_id").Scan(ctx, &ids); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.Store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.ExecContext(ctx, "SELECT id FROM licenses WHERE id=? FOR UPDATE", id); err != nil {
				return err
			}
			b := new(model.AIBinding)
			if err := tx.NewSelect().Model(b).Where("license_id=?", id).Scan(ctx); err != nil {
				return err
			}
			var err error
			b.Secret, err = rewrap(b.Secret, "key:"+id)
			if err != nil {
				return err
			}
			b.CandidateSecret, err = rewrap(b.CandidateSecret, "key:"+id)
			if err != nil {
				return err
			}
			_, err = tx.NewUpdate().Model(b).Column("secret", "candidate_secret").WherePK().Exec(ctx)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
