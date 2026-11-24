package service

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type AIAccountMetadata struct {
	Enabled       bool       `json:"enabled"`
	Configured    bool       `json:"configured"`
	State         string     `json:"state"`
	UpdatedAt     *time.Time `json:"updated_at"`
	ExpiresAt     *int64     `json:"expires_at"`
	LastRefreshAt *time.Time `json:"last_refresh_at"`
	HealthError   string     `json:"health_error,omitempty"`
	AlertStatus   string     `json:"alert_status,omitempty"`
}

// AccountMetadata never refreshes tokens or contacts the upstream provider.
func (s *AIService) AccountMetadata(ctx context.Context, productID string) (*AIAccountMetadata, error) {
	var row struct {
		Enabled       bool
		State         string
		UpdatedAt     time.Time
		Secret        []byte
		LastRefreshAt *time.Time
		HealthError   string
		Pending       int
		Sent          int
		Failed        int
	}
	err := s.Store.DB.NewRaw(`SELECT a.enabled, a.state, a.updated_at, a.secret, a.last_refresh_at, a.health_error,
        delivery.pending, delivery.sent, delivery.failed
        FROM ai_provider_accounts a
        CROSS JOIN LATERAL (
            SELECT count(*) FILTER (WHERE status = 'pending') AS pending,
                   count(*) FILTER (WHERE status = 'sent') AS sent,
                   count(*) FILTER (WHERE status = 'failed') AS failed
            FROM email_queue WHERE ai_alert_id = a.alert_id
        ) delivery WHERE a.product_id = ?`, productID).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return &AIAccountMetadata{State: "unconfigured"}, nil
	}
	if err != nil {
		return nil, err
	}
	metadata := &AIAccountMetadata{Enabled: row.Enabled, Configured: true, State: row.State,
		UpdatedAt: &row.UpdatedAt, LastRefreshAt: row.LastRefreshAt, HealthError: row.HealthError}
	var tokens AIAccountTokens
	if err := s.open(row.Secret, "account:"+productID, &tokens); err == nil {
		metadata.ExpiresAt = &tokens.ExpiresAt
	} else if row.HealthError != "credential_unreadable" {
		return nil, err
	}
	if row.Enabled && (row.State == "attention" || row.State == "refresh_uncertain") {
		switch {
		case row.Pending > 0:
			metadata.AlertStatus = "pending"
			if s.Email == nil || !s.Email.IsConfigured() {
				metadata.AlertStatus = "waiting_smtp"
			}
		case row.Failed > 0 && row.Sent > 0:
			metadata.AlertStatus = "partial"
		case row.Failed > 0:
			metadata.AlertStatus = "failed"
		case row.Sent > 0:
			metadata.AlertStatus = "sent"
		default:
			metadata.AlertStatus = "waiting_recipients"
		}
	}
	return metadata, nil
}
