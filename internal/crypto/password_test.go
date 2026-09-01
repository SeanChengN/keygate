package crypto

import (
	"strings"
	"testing"
)

func TestPasswordHashRoundTripAndPolicy(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if strings.Contains(hash, "correct-horse-battery-staple") {
		t.Fatal("password hash must not contain the plaintext password")
	}
	if !VerifyPassword(hash, "correct-horse-battery-staple") {
		t.Fatal("expected password verification to succeed")
	}
	if VerifyPassword(hash, "wrong-horse-battery-staple") {
		t.Fatal("wrong password must not verify")
	}
	if _, err := HashPassword("too-short"); err == nil {
		t.Fatal("short password must be rejected")
	}
	if _, err := HashPassword(strings.Repeat("x", 129)); err == nil {
		t.Fatal("oversized password must be rejected")
	}
}

func TestRecoveryCodeHashIsNormalizedAndDomainSeparated(t *testing.T) {
	pepper := strings.Repeat("p", 32)
	formatted := "abcd1234-efgh5678-ijkl9012"
	compact := "ABCD1234EFGH5678IJKL9012"
	if got, want := HashRecoveryCode(pepper, formatted), HashRecoveryCode(pepper, compact); got != want {
		t.Fatal("recovery code formatting must not affect its hash")
	}
	if HashRecoveryCode(pepper, formatted) == HashRecoveryCode(strings.Repeat("q", 32), formatted) {
		t.Fatal("different peppers must produce different hashes")
	}
}

func TestGenerateRecoveryCodesAreUnique(t *testing.T) {
	codes, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if len(code) != 26 || code[8] != '-' || code[17] != '-' {
			t.Fatalf("unexpected recovery code format %q", code)
		}
		if _, ok := seen[code]; ok {
			t.Fatalf("duplicate recovery code %q", code)
		}
		seen[code] = struct{}{}
	}
}
