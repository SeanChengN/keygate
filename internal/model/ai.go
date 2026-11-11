package model

import (
	"time"

	"github.com/uptrace/bun"
)

// AIPlanSettings are issuance defaults, never applied retroactively.
type AIPlanSettings struct {
	Enabled bool    `json:"enabled"`
	GroupID int64   `json:"group_id"`
	Quota   float64 `json:"quota"`
	Rate5H  float64 `json:"rate_limit_5h"`
	Rate1D  float64 `json:"rate_limit_1d"`
	Rate7D  float64 `json:"rate_limit_7d"`
}

// AICredential is exclusively carried in a device-bound signed token.
type AICredential struct {
	Version        int    `json:"version"`
	ProviderID     string `json:"provider_id"`
	ProfileBinding string `json:"profile_binding"`
	Revision       int64  `json:"revision"`
	APIKey         string `json:"api_key"`
}

type AIBinding struct {
	bun.BaseModel   `bun:"table:license_ai_bindings"`
	LicenseID       string         `bun:",pk" json:"license_id"`
	ProductID       string         `json:"product_id"`
	Config          AIPlanSettings `bun:"type:jsonb" json:"config"`
	ProfileBinding  string         `json:"profile_binding"`
	RemoteID        int64          `json:"remote_id"`
	Secret          []byte         `json:"-"`
	Revision        int64          `json:"revision"`
	Generation      int64          `json:"generation"`
	CandidateID     int64          `json:"-"`
	CandidateSecret []byte         `json:"-"`
	State           string         `json:"state"`
	LastError       string         `json:"last_error"`
	RemoteView      map[string]any `bun:"type:jsonb" json:"remote"`
	Anchor          time.Time      `json:"anchor"`
	Timezone        string         `json:"timezone"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type AIJob struct {
	bun.BaseModel `bun:"table:license_ai_jobs"`
	ID            string    `bun:",pk" json:"id"`
	LicenseID     string    `json:"license_id"`
	Action        string    `json:"action"`
	DedupeKey     string    `json:"dedupe_key"`
	State         string    `json:"state"`
	Phase         string    `json:"phase"`
	Attempts      int       `json:"attempts"`
	LastError     string    `json:"last_error"`
	CreatedAt     time.Time `json:"created_at"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
}
