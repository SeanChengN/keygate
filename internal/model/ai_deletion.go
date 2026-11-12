package model

import (
	"github.com/uptrace/bun"
	"time"
)

type AIDeleteTarget struct {
	ID          int64  `json:"id"`
	Fingerprint string `json:"fingerprint"`
	Verified    bool   `json:"verified"`
	Phase       string `json:"phase"`
}
type AIDeletion struct {
	bun.BaseModel   `bun:"table:license_ai_deletions"`
	ID              string           `bun:",pk" json:"id"`
	LicenseID       string           `json:"license_id"`
	ProductID       string           `json:"product_id"`
	AccountRevision int64            `json:"-"`
	ActorID         string           `json:"-"`
	IPAddress       string           `json:"-"`
	State           string           `json:"state"`
	Targets         []AIDeleteTarget `bun:"type:jsonb" json:"-"`
	LastErrorCode   string           `json:"last_error_code"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}
