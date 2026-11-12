package service

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type AIAccountMetadata struct {
	Enabled    bool       `json:"enabled"`
	Configured bool       `json:"configured"`
	State      string     `json:"state"`
	UpdatedAt  *time.Time `json:"updated_at"`
	ExpiresAt  *int64     `json:"expires_at"`
}

// AccountMetadata never refreshes tokens or contacts the upstream provider.
func (s *AIService) AccountMetadata(ctx context.Context, productID string) (*AIAccountMetadata, error) {
	var row struct {
		Enabled   bool
		State     string
		UpdatedAt time.Time
		Secret    []byte
	}
	err := s.Store.DB.NewSelect().Table("ai_provider_accounts").Column("enabled", "state", "updated_at", "secret").Where("product_id = ?", productID).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return &AIAccountMetadata{State: "unconfigured"}, nil
	}
	if err != nil {
		return nil, err
	}
	var tokens AIAccountTokens
	if err = s.open(row.Secret, "account:"+productID, &tokens); err != nil {
		return nil, err
	}
	return &AIAccountMetadata{Enabled: row.Enabled, Configured: true, State: row.State, UpdatedAt: &row.UpdatedAt, ExpiresAt: &tokens.ExpiresAt}, nil
}
