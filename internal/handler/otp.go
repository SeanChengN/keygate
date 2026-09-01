package handler

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
	"github.com/tabloy/keygate/pkg/response"
)

// OTPSend handles POST /api/v1/auth/otp/send
func (h *AuthHandler) OTPSend(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "valid email is required")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if h.Email == nil || !h.Email.IsConfigured() {
		response.Err(c, http.StatusServiceUnavailable, "OTP_UNAVAILABLE", "email OTP is not configured")
		return
	}

	// Rate limit: max 3 OTP requests per email per 10 minutes
	code := generateOTPCode()
	codeHash := hashOTPCode(h.Config.OTPPepper, code)

	otp := &model.OTPCode{
		Email:     email,
		CodeHash:  codeHash,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	if err := h.Store.CreateOTPForExistingUser(c, otp); err != nil {
		if errors.Is(err, store.ErrOTPUserNotFound) {
			// Enumeration-safe success: no code is persisted or delivered.
			response.OK(c, gin.H{"status": "sent"})
			return
		}
		if errors.Is(err, store.ErrOTPRateLimited) {
			response.Err(c, 429, "RATE_LIMITED", "too many code requests, try again later")
			return
		}
		response.Internal(c)
		return
	}
	h.Email.SendOTPCode(email, code)

	response.OK(c, gin.H{"status": "sent"})
}

// OTPVerify handles POST /api/v1/auth/otp/verify
func (h *AuthHandler) OTPVerify(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
		Code  string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "email and code are required")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	code := strings.TrimSpace(req.Code)

	presentedHash := hashOTPCode(h.Config.OTPPepper, code)
	if _, err := h.Store.ConsumeOTPCode(c, email, presentedHash); err != nil {
		response.Unauthorized(c, "invalid or expired code")
		return
	}

	user, err := h.Store.FindUserByEmail(c, email)
	if err != nil {
		response.Internal(c)
		return
	}

	// Auto-promote if email is in ADMIN_EMAILS
	if h.Config.IsAdminEmail(user.Email) && user.Role == model.RoleUser {
		_ = h.Store.SetUserRole(c, user.ID, model.RoleAdmin)
		user.Role = model.RoleAdmin
	}

	// Welcome email for new users
	if h.Email != nil && time.Since(user.CreatedAt) < time.Minute {
		h.Email.SendWelcome(user.Email, user.Name)
	}

	h.issueSession(c, user)

	h.Store.Audit(c, &model.AuditLog{
		Entity: "session", EntityID: user.ID, Action: "login",
		ActorType: "otp", ActorID: user.ID, IPAddress: c.ClientIP(),
		Changes: map[string]any{"email": user.Email},
	})

	response.OK(c, gin.H{
		"status": "ok", "email": user.Email, "name": user.Name,
		"is_admin": user.IsAdmin(), "role": user.Role,
	})
}

func generateOTPCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n.Int64())
}

func hashOTPCode(pepper, code string) string {
	mac := hmac.New(sha256.New, []byte("keygate/otp/v1/"+pepper))
	_, _ = mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}
