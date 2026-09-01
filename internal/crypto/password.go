package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	passwordMemory      = 64 * 1024
	passwordIterations  = 3
	passwordParallelism = 2
	passwordSaltBytes   = 16
	passwordKeyBytes    = 32
)

func HashPassword(password string) (string, error) {
	if len(password) < 16 || len(password) > 128 {
		return "", errors.New("password must be between 16 and 128 characters")
	}
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, passwordIterations, passwordMemory, passwordParallelism, passwordKeyBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, passwordMemory, passwordIterations, passwordParallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	if memory == 0 || memory > passwordMemory || iterations == 0 || iterations > 10 || parallelism == 0 || parallelism > 8 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != passwordSaltBytes {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != passwordKeyBytes {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func GenerateRecoveryCodes(count int) ([]string, error) {
	if count <= 0 {
		return nil, errors.New("recovery code count must be positive")
	}
	codes := make([]string, 0, count)
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	for range count {
		raw := make([]byte, 15)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		text := enc.EncodeToString(raw)
		codes = append(codes, strings.Join([]string{text[:8], text[8:16], text[16:24]}, "-"))
	}
	return codes, nil
}

func HashRecoveryCode(pepper, code string) string {
	mac := hmac.New(sha256.New, []byte("keygate/admin-recovery/v1/"+pepper))
	_, _ = mac.Write([]byte(strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))))
	return hex.EncodeToString(mac.Sum(nil))
}
