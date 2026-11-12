package model

import (
	"github.com/tabloy/keygate/pkg/apperr"
	"math"
)

// ValidateLimits is shared by handlers and persisted binding updates.
// An exact zero quota is unlimited; tiny positive values must never round to zero.
func (c AIPlanSettings) ValidateLimits() error {
	if c.GroupID <= 0 {
		return apperr.New(400, "AI_GROUP_INVALID", "a positive group ID is required")
	}
	if math.IsNaN(c.Quota) || math.IsInf(c.Quota, 0) || c.Quota < 0 || c.Quota > 1000000 || c.Quota > 0 && c.Quota < 0.00000001 {
		return apperr.New(400, "AI_QUOTA_INVALID", "quota must be zero or between 0.00000001 and 1000000 USD")
	}
	for _, v := range []float64{c.Rate5H, c.Rate1D, c.Rate7D} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1000000 {
			return apperr.New(400, "AI_RATE_INVALID", "invalid periodic USD limit")
		}
	}
	return nil
}
