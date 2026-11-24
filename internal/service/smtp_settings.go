package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/store"
	"github.com/uptrace/bun"
)

const (
	SMTPConfirmedKey      = "smtp_dashboard_confirmed"
	SMTPPasswordCipherKey = "smtp_password_cipher"
	smtpPasswordKey       = "smtp_password"
	smtpAAD               = "settings:smtp_password"
)

var smtpSettingKeys = []string{"smtp_host", "smtp_port", "smtp_username", smtpPasswordKey, "smtp_from",
	SMTPConfirmedKey, SMTPPasswordCipherKey}

var (
	ErrSMTPNotConfigured   = errors.New("SMTP is not configured")
	ErrSMTPInvalidSettings = errors.New("invalid SMTP settings")
	ErrSMTPEncryption      = errors.New("SMTP password encryption is unavailable")
)

type smtpConfig struct {
	host     string
	port     string
	username string
	password string
	from     string
}

func (c smtpConfig) present() bool { return c.host != "" && c.from != "" }

func dashboardSMTP(values map[string]string, password string) smtpConfig {
	port := strings.TrimSpace(values["smtp_port"])
	if port == "" {
		port = "587"
	} else if number, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(number)
	}
	return smtpConfig{
		host: strings.TrimSpace(values["smtp_host"]), port: port, from: strings.TrimSpace(values["smtp_from"]),
		username: strings.TrimSpace(values["smtp_username"]), password: password,
	}
}

func validDashboardSMTP(config smtpConfig) bool {
	if config.host == "" && config.from == "" {
		return config.username == ""
	}
	port, err := strconv.Atoi(config.port)
	if err != nil || port < 1 || port > 65535 || !config.present() ||
		(config.username == "") != (config.password == "") {
		return false
	}
	if net.ParseIP(config.host) == nil {
		for _, label := range strings.Split(strings.TrimSuffix(config.host, "."), ".") {
			if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return false
			}
			for _, r := range label {
				if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
					return false
				}
			}
		}
	}
	_, err = parseEnvelopeAddress(config.from)
	return err == nil && validateSMTPLine(config.from) == nil
}

func readSMTPSettings(ctx context.Context, db bun.IDB) (map[string]string, error) {
	var rows []store.Setting
	if err := db.NewSelect().Model(&rows).Where("key IN (?)", bun.In(smtpSettingKeys)).Scan(ctx); err != nil {
		return nil, err
	}
	values := make(map[string]string, len(rows))
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	return values, nil
}

// SMTPSource reports the configuration source without exposing credentials.
func (s *EmailService) SMTPSource(values map[string]string) string {
	if s != nil && s.environment.present() {
		return "environment"
	}
	if values[SMTPConfirmedKey] == "1" && dashboardSMTP(values, "").present() {
		return "dashboard"
	}
	if dashboardSMTP(values, "").present() {
		return "unconfirmed"
	}
	return "unconfigured"
}

func (s *EmailService) effectiveSMTP(ctx context.Context) (smtpConfig, error) {
	if s.environment.present() {
		return s.environment, nil
	}
	if s.store == nil {
		return smtpConfig{}, ErrSMTPNotConfigured
	}
	values, err := readSMTPSettings(ctx, s.store.DB)
	if err != nil {
		return smtpConfig{}, err
	}
	config := dashboardSMTP(values, "")
	if values[SMTPConfirmedKey] != "1" || !config.present() {
		return smtpConfig{}, ErrSMTPNotConfigured
	}
	config.password, err = s.decryptSMTPPassword(values[SMTPPasswordCipherKey])
	if err != nil {
		return smtpConfig{}, err
	}
	if !validDashboardSMTP(config) {
		return smtpConfig{}, ErrSMTPInvalidSettings
	}
	return config, nil
}

func (s *EmailService) decryptSMTPPassword(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if s.smtpAEAD == nil {
		return "", ErrSMTPEncryption
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", ErrSMTPEncryption
	}
	plain, err := s.smtpAEAD.Decrypt(raw, []byte(smtpAAD))
	if err != nil && s.previousSMTPAEAD != nil {
		plain, err = s.previousSMTPAEAD.Decrypt(raw, []byte(smtpAAD))
	}
	if err != nil {
		return "", ErrSMTPEncryption
	}
	defer clear(plain)
	return string(plain), nil
}

func (s *EmailService) encryptSMTPPassword(password string) (string, error) {
	if s.smtpAEAD == nil {
		return "", ErrSMTPEncryption
	}
	raw, err := s.smtpAEAD.Encrypt([]byte(password), []byte(smtpAAD))
	if err != nil {
		return "", ErrSMTPEncryption
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func (s *EmailService) ConfigureSMTPEncryption(ctx context.Context, key, previous *crypto.AESGCM) error {
	s.smtpAEAD, s.previousSMTPAEAD = key, previous
	if previous == nil || s.store == nil {
		return nil
	}
	return s.store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSMTPSettings(ctx, tx); err != nil {
			return err
		}
		var value string
		err := tx.NewRaw("SELECT value FROM settings WHERE key = ?", SMTPPasswordCipherKey).Scan(ctx, &value)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		password, err := s.decryptSMTPPassword(value)
		if err != nil {
			return err
		}
		ciphertext, err := s.encryptSMTPPassword(password)
		if err != nil {
			return err
		}
		return store.WriteSettings(ctx, tx, map[string]string{SMTPPasswordCipherKey: ciphertext})
	})
}

func lockSMTPSettings(ctx context.Context, tx bun.Tx) error {
	_, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('settings:smtp', 0))")
	return err
}

// SaveSMTPSettings commits the whole request together with SMTP validation and
// password protection. Legacy plaintext is consumed only on an explicit save.
func (s *EmailService) SaveSMTPSettings(ctx context.Context, settings map[string]string) error {
	writes := make(map[string]string, len(settings)+2)
	for k, v := range settings {
		writes[k] = v
	}
	return s.store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSMTPSettings(ctx, tx); err != nil {
			return err
		}
		values, err := readSMTPSettings(ctx, tx)
		if err != nil {
			return err
		}
		password := writes[smtpPasswordKey]
		delete(writes, smtpPasswordKey)
		for k, v := range writes {
			values[k] = v
		}
		if password == "" {
			password = values[smtpPasswordKey]
			if values[SMTPPasswordCipherKey] != "" {
				password, err = s.decryptSMTPPassword(values[SMTPPasswordCipherKey])
				if err != nil {
					return err
				}
			}
		}
		if !validDashboardSMTP(dashboardSMTP(values, password)) {
			return ErrSMTPInvalidSettings
		}
		if password != "" {
			ciphertext, err := s.encryptSMTPPassword(password)
			if err != nil {
				return err
			}
			writes[SMTPPasswordCipherKey] = ciphertext
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", smtpPasswordKey); err != nil {
			return err
		}
		writes[SMTPConfirmedKey] = "1"
		return store.WriteSettings(ctx, tx, writes)
	})
}

func (s *EmailService) ClearSMTPPassword(ctx context.Context) error {
	return s.store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSMTPSettings(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM settings WHERE key IN (?)", bun.In([]string{smtpPasswordKey, SMTPPasswordCipherKey}))
		return err
	})
}
