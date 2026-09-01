package store

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"time"

	"github.com/tabloy/keygate/internal/model"
)

var (
	ErrOTPUserNotFound = errors.New("otp user not found")
	ErrOTPRateLimited  = errors.New("otp request rate limited")
	ErrOTPInvalid      = errors.New("invalid otp")
)

func (s *Store) CreateOTPForExistingUser(ctx context.Context, otp *model.OTPCode) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.NewRaw("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "otp:"+otp.Email).Exec(ctx); err != nil {
		return err
	}
	exists, err := tx.NewSelect().Model((*model.User)(nil)).Where("email = ?", otp.Email).Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return ErrOTPUserNotFound
	}
	count, err := tx.NewSelect().Model((*model.OTPCode)(nil)).
		Where("email = ? AND created_at > ?", otp.Email, time.Now().Add(-10*time.Minute)).Count(ctx)
	if err != nil {
		return err
	}
	if count >= 3 {
		return ErrOTPRateLimited
	}
	if otp.ID == "" {
		otp.ID = newID()
	}
	if _, err := tx.NewInsert().Model(otp).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumeOTPCode serializes verification of the latest active code. Attempts
// and one-time consumption are committed atomically so concurrent verifies
// cannot both establish a session.
func (s *Store) ConsumeOTPCode(ctx context.Context, email, presentedHash string) (*model.OTPCode, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	otp := new(model.OTPCode)
	err = tx.NewSelect().Model(otp).
		Where("email = ? AND used = false AND expires_at > now()", email).
		OrderExpr("created_at DESC").Limit(1).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrOTPInvalid
	}
	if err != nil {
		return nil, err
	}
	nextAttempts := otp.Attempts + 1
	match := hmac.Equal([]byte(otp.CodeHash), []byte(presentedHash)) && nextAttempts <= 5
	if match {
		if _, err := tx.NewUpdate().Model((*model.OTPCode)(nil)).Set("attempts = ?, used = true", nextAttempts).Where("id = ?", otp.ID).Exec(ctx); err != nil {
			return nil, err
		}
		return otp, tx.Commit()
	}
	if _, err := tx.NewUpdate().Model((*model.OTPCode)(nil)).Set("attempts = ?, used = ?", nextAttempts, nextAttempts >= 5).Where("id = ?", otp.ID).Exec(ctx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return nil, ErrOTPInvalid
}
