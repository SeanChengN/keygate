package service

import (
	"errors"
	"github.com/tabloy/keygate/pkg/apperr"
	"strings"
)

// AIErrorCode classifies current and historical sanitized failures. Unknown
// messages stay private; UI clients translate the stable code, never raw text.
func AIErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var app *apperr.AppError
	if errors.As(err, &app) {
		return app.Code
	}
	if errors.Is(err, ErrAIConfigChanged) {
		return "AI_CONFIG_CHANGED"
	}
	msg := err.Error()
	if msg == "AI_DELETE_IN_PROGRESS" {
		return "AI_DELETE_IN_PROGRESS"
	}
	switch {
	case strings.Contains(msg, "zero grace"):
		return "AI_PLAN_INELIGIBLE"
	case strings.Contains(msg, "account is not configured"):
		return "AI_ACCOUNT_UNCONFIGURED"
	case strings.Contains(msg, "account needs administrator"), strings.Contains(msg, "refresh needs administrator"):
		return "AI_ACCOUNT_ATTENTION"
	case strings.Contains(msg, "future expires_at"):
		return "AI_TOKENS_INVALID"
	case strings.Contains(msg, "encryption is not configured"):
		return "AI_DISABLED"
	case strings.Contains(msg, "cannot be decrypted"), strings.Contains(msg, "credential is invalid"):
		return "AI_CREDENTIAL_INVALID"
	case strings.Contains(msg, "studio.ai_profile"), strings.Contains(msg, "AI profile"), strings.Contains(msg, "plan route changed"):
		return "AI_PROFILE_INVALID"
	case strings.Contains(msg, "positive monthly"), strings.Contains(msg, "valid AI limits"):
		return "AI_QUOTA_INVALID"
	case strings.Contains(msg, "invalid USD limit"):
		return "AI_RATE_INVALID"
	case strings.Contains(msg, "reset result unknown"), strings.Contains(msg, "reset readback unknown"), strings.Contains(msg, "manual resolution"), strings.Contains(msg, "earlier operation"):
		return "AI_RESULT_UNCERTAIN"
	case strings.Contains(msg, "pending or uncertain"):
		return "AI_OPERATION_BUSY"
	case strings.Contains(msg, "idempotency"), strings.Contains(msg, "Idempotency"), strings.Contains(msg, "duplicate license IDs"), strings.Contains(msg, "up to 500"):
		return "AI_REQUEST_INVALID"
	case strings.Contains(msg, "readback"), strings.Contains(msg, "identity mismatch"):
		return "AI_READBACK_FAILED"
	case strings.Contains(msg, "provider request failed"):
		switch {
		case strings.Contains(msg, "insufficient_quota"):
			return "insufficient_quota"
		case strings.Contains(msg, "API_KEY_DISABLED"):
			return "API_KEY_DISABLED"
		case strings.Contains(msg, "API_KEY_EXPIRED"):
			return "API_KEY_EXPIRED"
		case strings.Contains(msg, "(401,"), strings.Contains(msg, "(403,"):
			return "AI_ACCOUNT_ATTENTION"
		case strings.Contains(msg, "(429,"):
			return "AI_PROVIDER_RATE_LIMITED"
		default:
			return "AI_PROVIDER_FAILED"
		}
	default:
		return "AI_OPERATION_FAILED"
	}
}
