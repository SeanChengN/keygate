package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	keycrypto "github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
)

func TestRecoverAdminByOperator_Atomic(t *testing.T) {
	s := setupTestDB(t)
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	email := "operator-recovery-" + time.Now().Format("150405.000000") + "@example.com"
	user := &model.User{ID: store.NewID(), Email: email, Name: "Recovery Owner", Role: model.RoleOwner}
	if _, err := s.DB.NewInsert().Model(user).Exec(ctx); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	if _, err := s.DB.NewRaw(
		"INSERT INTO admin_recovery_codes (id, user_id, code_hash) VALUES (?, ?, ?)",
		store.NewID(), user.ID, "superseded-recovery-hash",
	).Exec(ctx); err != nil {
		t.Fatalf("insert superseded recovery code: %v", err)
	}
	if err := s.CreateRefreshToken(ctx, user.ID, "superseded-refresh-token-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("insert refresh token: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.DB.NewRaw("DELETE FROM audit_logs WHERE entity_id = ?", user.ID).Exec(ctx)
		_, _ = s.DB.NewRaw("DELETE FROM admin_recovery_codes WHERE user_id = ?", user.ID).Exec(ctx)
		_, _ = s.DB.NewRaw("DELETE FROM refresh_tokens WHERE user_id = ?", user.ID).Exec(ctx)
		_, _ = s.DB.NewRaw("DELETE FROM users WHERE id = ?", user.ID).Exec(ctx)
	})

	passwordHash, err := keycrypto.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	hashes := []string{"recovery-hash-one", "recovery-hash-two"}
	recovered, err := s.RecoverAdminByOperator(ctx, email, passwordHash, hashes)
	if err != nil {
		t.Fatalf("RecoverAdminByOperator() error = %v", err)
	}
	if recovered.ID != user.ID || recovered.AuthGeneration != 1 {
		t.Fatalf("recovered user = %#v", recovered)
	}
	stored, err := s.FindUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("find recovered owner: %v", err)
	}
	if !keycrypto.VerifyPassword(stored.PasswordHash, "correct horse battery staple") {
		t.Fatal("stored password hash does not verify")
	}
	var codeCount int
	if err := s.DB.NewRaw("SELECT count(*) FROM admin_recovery_codes WHERE user_id = ? AND used_at IS NULL", user.ID).Scan(ctx, &codeCount); err != nil {
		t.Fatalf("count recovery codes: %v", err)
	}
	if codeCount != len(hashes) {
		t.Fatalf("active recovery codes = %d, want %d", codeCount, len(hashes))
	}
	var supersededCodeCount int
	if err := s.DB.NewRaw("SELECT count(*) FROM admin_recovery_codes WHERE user_id = ? AND code_hash = ? AND used_at IS NOT NULL", user.ID, "superseded-recovery-hash").Scan(ctx, &supersededCodeCount); err != nil {
		t.Fatalf("count superseded recovery codes: %v", err)
	}
	if supersededCodeCount != 1 {
		t.Fatalf("superseded recovery codes = %d, want 1", supersededCodeCount)
	}
	var refreshTokenCount int
	if err := s.DB.NewRaw("SELECT count(*) FROM refresh_tokens WHERE user_id = ?", user.ID).Scan(ctx, &refreshTokenCount); err != nil {
		t.Fatalf("count refresh tokens: %v", err)
	}
	if refreshTokenCount != 0 {
		t.Fatalf("refresh tokens = %d, want 0", refreshTokenCount)
	}
	var auditCount int
	if err := s.DB.NewRaw("SELECT count(*) FROM audit_logs WHERE entity_id = ? AND action = 'admin_operator_recovered' AND actor_type = 'operator'", user.ID).Scan(ctx, &auditCount); err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit logs = %d, want 1", auditCount)
	}
	if _, err := s.RecoverAdminByOperator(ctx, email, passwordHash, []string{""}); err == nil {
		t.Fatal("empty recovery hash error = nil")
	}
	stored, err = s.FindUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("find owner after rollback: %v", err)
	}
	if stored.AuthGeneration != 1 {
		t.Fatalf("auth generation after rollback = %d, want 1", stored.AuthGeneration)
	}
	if err := s.DB.NewRaw("SELECT count(*) FROM admin_recovery_codes WHERE user_id = ? AND used_at IS NULL", user.ID).Scan(ctx, &codeCount); err != nil || codeCount != len(hashes) {
		t.Fatalf("active recovery codes after rollback = %d, err = %v", codeCount, err)
	}
	if _, err := s.RecoverAdminByOperator(ctx, "missing@example.com", passwordHash, hashes); !errors.Is(err, store.ErrAdminNotFound) {
		t.Fatalf("missing admin error = %v, want ErrAdminNotFound", err)
	}
}
