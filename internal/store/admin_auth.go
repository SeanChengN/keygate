package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/tabloy/keygate/internal/model"
)

var ErrRecoveryCodeInvalid = errors.New("invalid recovery code")

func (s *Store) SetAdminPassword(ctx context.Context, userID, passwordHash string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	now := time.Now()
	result, err := tx.NewUpdate().Model((*model.User)(nil)).
		Set("password_hash = ?, password_changed_at = ?, auth_generation = auth_generation + 1, updated_at = ?", passwordHash, now, now).
		Where("id = ? AND role IN ('owner', 'admin')", userID).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return sql.ErrNoRows
	}
	if _, err := tx.NewRaw("DELETE FROM refresh_tokens WHERE user_id = ?", userID).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID string, hashes []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.NewRaw("UPDATE admin_recovery_codes SET used_at = now() WHERE user_id = ? AND used_at IS NULL", userID).Exec(ctx); err != nil {
		return err
	}
	for _, hash := range hashes {
		if _, err := tx.NewRaw("INSERT INTO admin_recovery_codes (id, user_id, code_hash) VALUES (?, ?, ?)", newID(), userID, hash).Exec(ctx); err != nil {
			return err
		}
	}
	if _, err := tx.NewRaw("UPDATE users SET auth_generation = auth_generation + 1, updated_at = now() WHERE id = ? AND role IN ('owner', 'admin')", userID).Exec(ctx); err != nil {
		return err
	}
	if _, err := tx.NewRaw("DELETE FROM refresh_tokens WHERE user_id = ?", userID).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ConsumeRecoveryCode(ctx context.Context, email, codeHash, newPasswordHash string) (*model.User, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	user := new(model.User)
	if err := tx.NewSelect().Model(user).
		Where("email = ? AND role IN ('owner', 'admin')", email).
		For("UPDATE").Scan(ctx); err != nil {
		return nil, ErrRecoveryCodeInvalid
	}
	var codeID string
	if err := tx.NewRaw("SELECT id FROM admin_recovery_codes WHERE user_id = ? AND code_hash = ? AND used_at IS NULL FOR UPDATE", user.ID, codeHash).Scan(ctx, &codeID); err != nil {
		return nil, ErrRecoveryCodeInvalid
	}
	now := time.Now()
	if _, err := tx.NewRaw("UPDATE users SET password_hash = ?, password_changed_at = ?, auth_generation = auth_generation + 1, updated_at = ? WHERE id = ?", newPasswordHash, now, now, user.ID).Exec(ctx); err != nil {
		return nil, err
	}
	if _, err := tx.NewRaw("UPDATE admin_recovery_codes SET used_at = ? WHERE user_id = ? AND used_at IS NULL", now, user.ID).Exec(ctx); err != nil {
		return nil, err
	}
	if _, err := tx.NewRaw("DELETE FROM refresh_tokens WHERE user_id = ?", user.ID).Exec(ctx); err != nil {
		return nil, err
	}
	user.PasswordHash = newPasswordHash
	user.PasswordChangedAt = &now
	user.AuthGeneration++
	return user, tx.Commit()
}
