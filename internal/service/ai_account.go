package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/tabloy/keygate/pkg/apperr"
	"github.com/uptrace/bun"
)

const (
	aiAccountCheckInterval = time.Minute
	aiAccountRefreshWindow = 5 * time.Minute
)

var errAIAccountAttention = errors.New("jiji account needs administrator attention")

type aiAccount struct {
	AIAccountTokens
	secret             []byte
	credentialRevision int64
}

func (s *AIService) Account(ctx context.Context, productID string) (*AIAccountTokens, error) {
	account, err := s.account(ctx, productID, false)
	if err != nil {
		return nil, err
	}
	return &account.AIAccountTokens, nil
}

func (s *AIService) account(ctx context.Context, productID string, monitor bool) (*aiAccount, error) {
	conn, err := s.Store.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	lockKey := "ai-account:" + productID
	locked := true
	if monitor {
		err = conn.NewRaw("SELECT pg_try_advisory_lock(hashtextextended(?, 0))", lockKey).Scan(ctx, &locked)
	} else {
		_, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtextextended(?, 0))", lockKey)
	}
	if err != nil {
		discardLockedConnection(conn)
		return nil, err
	}
	if !locked {
		return nil, nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, "SELECT pg_advisory_unlock(hashtextextended(?, 0))", lockKey); err != nil {
			discardLockedConnection(conn)
		}
	}()

	var row struct {
		Secret             []byte
		Enabled            bool
		State              string
		CredentialRevision int64
	}
	err = conn.NewSelect().Table("ai_provider_accounts").Column("secret", "enabled", "state", "credential_revision").
		Where("product_id = ?", productID).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("jiji account is not configured")
	}
	if err != nil {
		return nil, err
	}
	if !row.Enabled {
		return nil, errAIAccountAttention
	}
	if row.State == "refreshing" {
		// This lock excludes any active rotator. The persisted marker came from
		// an interrupted request and the saved token may already be invalid.
		return nil, setAIAccountIssue(ctx, conn, productID, "refresh_uncertain", "refresh_interrupted")
	}
	if row.State != "ready" {
		return nil, errAIAccountAttention
	}
	result := &aiAccount{secret: row.Secret, credentialRevision: row.CredentialRevision}
	if err := s.open(row.Secret, "account:"+productID, &result.AIAccountTokens); err != nil {
		return nil, setAIAccountIssue(ctx, conn, productID, "attention", "credential_unreadable")
	}
	if time.Unix(result.ExpiresAt, 0).After(time.Now().Add(aiAccountRefreshWindow)) {
		return result, nil
	}

	// Autocommit the marker before requesting a rotation, while retaining the
	// session lock. It survives process death without requiring a second
	// pooled connection. SaveAccount takes the same lock in transaction mode.
	marker, err := conn.ExecContext(ctx, `UPDATE ai_provider_accounts SET state = 'refreshing'
        WHERE product_id = ? AND enabled AND state = 'ready' AND secret = ?`, productID, row.Secret)
	if err != nil {
		return nil, err
	}
	count, err := marker.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, errAIAccountAttention
	}
	next, err := s.Client.Refresh(ctx, &result.AIAccountTokens)
	if err != nil {
		var provider *AIProviderError
		if errors.As(err, &provider) && !provider.Uncertain && provider.Status >= 400 && provider.Status < 500 {
			return nil, setAIAccountIssue(ctx, conn, productID, "attention", "refresh_rejected")
		}
		return nil, setAIAccountIssue(ctx, conn, productID, "refresh_uncertain", "refresh_result_unknown")
	}
	raw, err := s.seal(next, "account:"+productID)
	if err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, `UPDATE ai_provider_accounts SET secret = ?, state = 'ready',
        health_error = '', alert_id = NULL, updated_at = now(), last_refresh_at = now()
        WHERE product_id = ?`, raw, productID); err != nil {
		return nil, err
	}
	result.AIAccountTokens = *next
	result.secret = raw
	return result, nil
}

func discardLockedConnection(conn bun.Conn) {
	// An uncertain lock/unlock cannot return a possibly locked session to the pool.
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}

func setAIAccountIssue(ctx context.Context, conn bun.Conn, productID, state, reason string) error {
	_, err := conn.ExecContext(ctx, `UPDATE ai_provider_accounts SET state = ?, health_error = ?,
        alert_id = COALESCE(alert_id, ?::uuid), updated_at = clock_timestamp() WHERE product_id = ?`,
		state, reason, uuid.NewString(), productID)
	if err != nil {
		return err
	}
	return errAIAccountAttention
}

func (s *AIService) noteAccountAuthFailure(ctx context.Context, productID string, account *aiAccount) {
	if account == nil {
		return
	}
	// The ciphertext changes on every rotation or replacement. An old access
	// token's rejection must not mark the newer token pair as unhealthy.
	if _, err := s.Store.DB.ExecContext(ctx, `UPDATE ai_provider_accounts SET state = 'attention',
        health_error = 'access_rejected', alert_id = ?::uuid, updated_at = clock_timestamp()
        WHERE product_id = ? AND enabled AND state = 'ready' AND secret = ?`,
		uuid.NewString(), productID, account.secret); err != nil {
		slog.Warn("could not record jiji account authentication failure", "product_id", productID)
	}
}

func (s *AIService) StartAccountMonitor(ctx context.Context) {
	if s.AEAD == nil || s.Client == nil {
		return
	}
	tick := time.NewTicker(aiAccountCheckInterval)
	defer tick.Stop()
	for {
		s.inspectAccounts(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *AIService) inspectAccounts(ctx context.Context) {
	var ids []string
	if err := s.Store.DB.NewSelect().Table("ai_provider_accounts").Column("product_id").
		Where("enabled").OrderExpr("product_id").Scan(ctx, &ids); err != nil {
		slog.Warn("could not list enabled jiji accounts")
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.account(ctx, id, true); err != nil && !errors.Is(err, errAIAccountAttention) {
			slog.Warn("could not inspect jiji account", "product_id", id)
		}
		if err := s.enqueueAccountAlert(ctx, id); err != nil {
			slog.Warn("could not queue jiji account alert", "product_id", id)
		}
	}
}

func (s *AIService) enqueueAccountAlert(ctx context.Context, productID string) error {
	return s.Store.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var row struct {
			HealthError string
			UpdatedAt   time.Time
			AlertID     string
			Name        string
		}
		err := tx.NewRaw(`SELECT a.health_error, a.updated_at, COALESCE(a.alert_id::text, '') AS alert_id, p.name
            FROM ai_provider_accounts a JOIN products p ON p.id = a.product_id
            WHERE a.product_id = ? AND a.enabled AND a.state IN ('attention', 'refresh_uncertain')
            FOR UPDATE OF a`, productID).Scan(ctx, &row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.AlertID == "" {
			row.AlertID = uuid.NewString()
			if _, err := tx.ExecContext(ctx, "UPDATE ai_provider_accounts SET alert_id = ?::uuid WHERE product_id = ?", row.AlertID, productID); err != nil {
				return err
			}
		}
		var owners []string
		if err := tx.NewRaw("SELECT DISTINCT lower(trim(email)) FROM users WHERE role = 'owner'").Scan(ctx, &owners); err != nil {
			return err
		}
		reason := "The Jiji account needs attention."
		switch row.HealthError {
		case "refresh_rejected":
			reason = "Jiji rejected the token renewal."
		case "refresh_interrupted", "refresh_result_unknown":
			reason = "The result of the token renewal could not be confirmed."
		case "credential_unreadable":
			reason = "Keygate could not read the saved credentials."
		case "access_rejected":
			reason = "The provider refused a request using the saved credentials."
		}
		body := renderTemplate(aiAccountAlertTemplate, map[string]string{"Product": row.Name, "Reason": reason, "OccurredAt": row.UpdatedAt.UTC().Format(time.RFC3339)})
		for _, owner := range owners {
			if apperr.ValidateEmail(owner) != nil {
				continue
			}
			address, err := parseEnvelopeAddress(owner)
			if err != nil {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO email_queue (id, to_addr, subject, body, ai_alert_id)
                VALUES (?, ?, ?, ?, ?::uuid) ON CONFLICT DO NOTHING`,
				uuid.NewString(), address, "Keygate: Jiji account needs attention", body, row.AlertID); err != nil {
				return err
			}
		}
		return nil
	})
}

const aiAccountAlertTemplate = `<!doctype html>
<html><body>
<h2>Jiji account needs attention</h2>
<p>Product: {{.Product}}</p>
<p>Recorded at: {{.OccurredAt}}</p>
<p>{{.Reason}}</p>
<p>Open Keygate → Products → Jiji management account to check its current status. If it still needs attention, sign in to Jiji, save a fresh token pair, and review the affected synchronization jobs.</p>
</body></html>`
