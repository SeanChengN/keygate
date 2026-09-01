package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	keycrypto "github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/middleware"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/pkg/response"
)

var dummyAdminPasswordHash, _ = keycrypto.HashPassword("keygate-dummy-password-never-valid")

func adminLoginBudget(c *gin.Context, email string) bool {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	emailKey := hex.EncodeToString(digest[:])
	return middleware.AllowRateLimit("admin-password:ip:"+c.ClientIP(), 20, 15*time.Minute) &&
		middleware.AllowRateLimit("admin-password:email:"+emailKey, 10, 15*time.Minute)
}

func (h *AuthHandler) AdminPasswordLogin(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Unauthorized(c, "invalid credentials")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !adminLoginBudget(c, req.Email) {
		response.Err(c, http.StatusTooManyRequests, "RATE_LIMITED", "authentication temporarily unavailable")
		return
	}
	user, err := h.Store.FindUserByEmail(c, req.Email)
	hash := dummyAdminPasswordHash
	if err == nil && user != nil && user.PasswordHash != "" {
		hash = user.PasswordHash
	}
	valid := keycrypto.VerifyPassword(hash, req.Password)
	if err != nil || user == nil || !user.IsAdmin() || user.PasswordHash == "" || !valid {
		response.Unauthorized(c, "invalid credentials")
		return
	}
	h.Store.DeleteUserRefreshTokens(c, user.ID)
	h.issueSession(c, user)
	h.Store.Audit(c, &model.AuditLog{
		Entity: "session", EntityID: user.ID, Action: "admin_password_login",
		ActorType: "admin", ActorID: user.ID, IPAddress: c.ClientIP(),
	})
	response.OK(c, gin.H{"status": "ok", "email": user.Email, "name": user.Name, "is_admin": true, "role": user.Role})
}

func (h *AuthHandler) AdminRecover(c *gin.Context) {
	var req struct {
		Email        string `json:"email" binding:"required,email"`
		RecoveryCode string `json:"recovery_code" binding:"required"`
		NewPassword  string `json:"new_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Unauthorized(c, "invalid recovery request")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !adminLoginBudget(c, req.Email) {
		response.Err(c, http.StatusTooManyRequests, "RATE_LIMITED", "authentication temporarily unavailable")
		return
	}
	passwordHash, err := keycrypto.HashPassword(req.NewPassword)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	codeHash := keycrypto.HashRecoveryCode(h.Config.OTPPepper, req.RecoveryCode)
	user, err := h.Store.ConsumeRecoveryCode(c, req.Email, codeHash, passwordHash)
	if err != nil {
		response.Unauthorized(c, "invalid recovery request")
		return
	}
	h.issueSession(c, user)
	h.Store.Audit(c, &model.AuditLog{
		Entity: "user", EntityID: user.ID, Action: "admin_password_recovered",
		ActorType: "admin", ActorID: user.ID, IPAddress: c.ClientIP(),
	})
	response.OK(c, gin.H{"status": "ok"})
}

func (h *AuthHandler) ChangeAdminPassword(c *gin.Context) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "new_password is required")
		return
	}
	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)
	user, err := h.Store.FindUserByID(c, uid)
	if err != nil || !verifyCurrentAdminPassword(user, req.CurrentPassword) {
		response.Unauthorized(c, "invalid credentials")
		return
	}
	passwordHash, err := keycrypto.HashPassword(req.NewPassword)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.Store.SetAdminPassword(c, uid, passwordHash); err != nil {
		response.Internal(c)
		return
	}
	user, err = h.Store.FindUserByID(c, uid)
	if err != nil {
		response.Internal(c)
		return
	}
	h.issueSession(c, user)
	h.Store.Audit(c, &model.AuditLog{
		Entity: "user", EntityID: uid, Action: "admin_password_changed",
		ActorType: "admin", ActorID: uid, IPAddress: c.ClientIP(),
	})
	response.OK(c, gin.H{"status": "ok"})
}

func verifyCurrentAdminPassword(user *model.User, currentPassword string) bool {
	if user == nil || !user.IsAdmin() {
		return false
	}
	// Existing installations predate local administrator passwords. A valid
	// administrator session (normally established through email OTP) may set
	// the first password. Once a hash exists, every later change requires the
	// current password as usual.
	if user.PasswordHash == "" {
		return true
	}
	return currentPassword != "" && keycrypto.VerifyPassword(user.PasswordHash, currentPassword)
}

func (h *AuthHandler) RotateRecoveryCodes(c *gin.Context) {
	var req struct {
		CurrentPassword string `json:"current_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "current_password is required")
		return
	}
	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)
	user, err := h.Store.FindUserByID(c, uid)
	if err != nil || !user.IsAdmin() || !keycrypto.VerifyPassword(user.PasswordHash, req.CurrentPassword) {
		response.Unauthorized(c, "invalid credentials")
		return
	}
	codes, err := keycrypto.GenerateRecoveryCodes(10)
	if err != nil {
		response.Internal(c)
		return
	}
	hashes := make([]string, len(codes))
	for i, code := range codes {
		hashes[i] = keycrypto.HashRecoveryCode(h.Config.OTPPepper, code)
	}
	if err := h.Store.ReplaceRecoveryCodes(c, uid, hashes); err != nil {
		response.Internal(c)
		return
	}
	user, err = h.Store.FindUserByID(c, uid)
	if err != nil {
		response.Internal(c)
		return
	}
	h.issueSession(c, user)
	c.Header("Cache-Control", "no-store")
	h.Store.Audit(c, &model.AuditLog{
		Entity: "user", EntityID: uid, Action: "admin_recovery_codes_rotated",
		ActorType: "admin", ActorID: uid, IPAddress: c.ClientIP(),
	})
	response.OK(c, gin.H{"recovery_codes": codes})
}
