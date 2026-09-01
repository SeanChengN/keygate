package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/license"
	"github.com/tabloy/keygate/internal/model"
)

const keyStorageMigrationLock int64 = 0x6b677365637572 // "kgsecur"

// MigrateKeyStorage validates every protected row and re-encrypts it under the
// current master-key-derived AEAD. The transaction-scoped advisory lock makes
// concurrent application starts serialize the entire migration.
func (s *Store) MigrateKeyStorage(
	ctx context.Context,
	mode string,
	currentLicense, previousLicense *crypto.AESGCM,
	currentRelease, previousRelease *crypto.AESGCM,
) error {
	if mode != "dual" && mode != "ciphertext_only" {
		return fmt.Errorf("unsupported license key storage mode %q", mode)
	}
	if currentLicense == nil || currentRelease == nil {
		return errors.New("current encryption key is required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(?)", keyStorageMigrationLock); err != nil {
		return fmt.Errorf("acquire key migration lock: %w", err)
	}
	var persisted string
	err = tx.NewRaw("SELECT value FROM security_state WHERE key = 'license_key_storage_mode'").Scan(ctx, &persisted)
	if err != nil && !isNoRows(err) {
		return err
	}
	if persisted == "ciphertext_only" && mode != "ciphertext_only" {
		return errors.New("database is ciphertext_only and cannot return to dual mode")
	}
	if mode == "ciphertext_only" {
		// Dropping these inside the transaction is reversible if any later
		// validation fails, and permits every plaintext column to become ''.
		if _, err := tx.ExecContext(ctx, "ALTER TABLE licenses DROP CONSTRAINT IF EXISTS licenses_license_key_key"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DROP INDEX IF EXISTS idx_licenses_key"); err != nil {
			return err
		}
	}

	var licenses []*model.License
	if err := tx.NewSelect().Model(&licenses).Scan(ctx); err != nil {
		return err
	}
	for _, row := range licenses {
		plaintext, err := decryptWithFallback(row.LicenseKeyEncrypted, []byte(row.ID), currentLicense, previousLicense)
		if err != nil && row.LicenseKey != "" {
			plaintext = []byte(row.LicenseKey)
			err = nil
		}
		if err != nil || len(plaintext) == 0 {
			return fmt.Errorf("license %s has no decryptable key", row.ID)
		}
		ciphertext, err := currentLicense.Encrypt(plaintext, []byte(row.ID))
		if err != nil {
			return fmt.Errorf("re-encrypt license %s: %w", row.ID, err)
		}
		plaintextColumn := row.LicenseKey
		if mode == "dual" {
			plaintextColumn = string(plaintext)
		} else {
			plaintextColumn = ""
		}
		if _, err := tx.NewUpdate().Model((*model.License)(nil)).
			Set("license_key = ?, key_hash = ?, license_key_encrypted = ?", plaintextColumn, license.HashKey(string(plaintext)), ciphertext).
			Where("id = ?", row.ID).Exec(ctx); err != nil {
			return err
		}
		clear(plaintext)
	}

	var signingKeys []*model.ReleaseSigningKey
	if err := tx.NewSelect().Model(&signingKeys).Scan(ctx); err != nil {
		return err
	}
	for _, row := range signingKeys {
		seed, err := decryptWithFallback(row.PrivateKeyEncrypted, []byte(row.ProductID), currentRelease, previousRelease)
		if err != nil {
			return fmt.Errorf("release signing key %s is not decryptable: %w", row.ID, err)
		}
		ciphertext, err := currentRelease.Encrypt(seed, []byte(row.ProductID))
		clear(seed)
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*model.ReleaseSigningKey)(nil)).
			Set("private_key_encrypted = ?", ciphertext).Where("id = ?", row.ID).Exec(ctx); err != nil {
			return err
		}
	}

	if mode == "ciphertext_only" {
		var constraintExists bool
		if err := tx.NewRaw("SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'licenses_ciphertext_only' AND conrelid = 'licenses'::regclass)").Scan(ctx, &constraintExists); err != nil {
			return err
		}
		if !constraintExists {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE licenses ADD CONSTRAINT licenses_ciphertext_only CHECK (license_key = '' AND key_hash <> '' AND license_key_encrypted IS NOT NULL) NOT VALID`); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "ALTER TABLE licenses VALIDATE CONSTRAINT licenses_ciphertext_only"); err != nil {
			return err
		}
	}
	if _, err := tx.NewRaw(`INSERT INTO security_state (key, value, updated_at) VALUES ('license_key_storage_mode', ?, now()) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, mode).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func decryptWithFallback(ciphertext, aad []byte, current, previous *crypto.AESGCM) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, errors.New("ciphertext missing")
	}
	if current != nil {
		if plaintext, err := current.Decrypt(ciphertext, aad); err == nil {
			return plaintext, nil
		}
	}
	if previous != nil {
		if plaintext, err := previous.Decrypt(ciphertext, aad); err == nil {
			return plaintext, nil
		}
	}
	return nil, errors.New("ciphertext cannot be decrypted with current or previous key")
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
