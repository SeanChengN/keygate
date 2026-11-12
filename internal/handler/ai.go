package handler

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/service"
	"github.com/tabloy/keygate/internal/store"
	"github.com/tabloy/keygate/pkg/response"
	"github.com/uptrace/bun"
)

func (h *AdminHandler) aiReady(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	if h.AI == nil || h.AI.AEAD == nil {
		response.Err(c, 503, "AI_DISABLED", "AI credential encryption is not configured")
		return false
	}
	return true
}
func (h *AdminHandler) aiProduct(c *gin.Context, id string) bool {
	p, err := h.Store.FindProductByID(c, id)
	if err != nil || p.Slug != "movaura-studio" || p.Type != "desktop" {
		response.Err(c, 400, "AI_PRODUCT_INVALID", "AI provisioning is restricted to the Movaura Studio desktop product")
		return false
	}
	return requireKeyProductScope(c, id)
}

func (h *AdminHandler) GetAIAccount(c *gin.Context) {
	if !h.aiReady(c) || !h.aiProduct(c, c.Param("id")) {
		return
	}
	metadata, err := h.AI.AccountMetadata(c, c.Param("id"))
	if err != nil {
		aiError(c, err, 500)
		return
	}
	response.OK(c, metadata)
}
func (h *AdminHandler) PutAIAccount(c *gin.Context) {
	if !h.aiReady(c) || !h.aiProduct(c, c.Param("id")) {
		return
	}
	var req struct {
		Enabled      bool   `json:"enabled"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    int64  `json:"expires_at"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.Err(c, 400, "AI_REQUEST_INVALID", "invalid account configuration")
		return
	}
	var tokens *service.AIAccountTokens
	if req.AccessToken != "" || req.RefreshToken != "" {
		if len(req.AccessToken) > 16384 || len(req.RefreshToken) > 16384 {
			response.Err(c, 400, "AI_TOKENS_INVALID", "token is too long")
			return
		}
		tokens = &service.AIAccountTokens{AccessToken: req.AccessToken, RefreshToken: req.RefreshToken, ExpiresAt: req.ExpiresAt}
	}
	if err := h.AI.SaveAccount(c, c.Param("id"), req.Enabled, tokens); err != nil {
		aiError(c, err, 400)
		return
	}
	h.Store.Audit(c, &model.AuditLog{Entity: "product", EntityID: c.Param("id"), Action: "ai_account_updated", ActorType: "admin", ActorID: adminID(c)})
	h.GetAIAccount(c)
}

func (h *AdminHandler) GetAIPlan(c *gin.Context) {
	if !h.aiReady(c) {
		return
	}
	p, err := h.Store.FindPlanByID(c, c.Param("id"))
	if err != nil {
		response.NotFound(c, "plan not found")
		return
	}
	if !h.aiProduct(c, p.ProductID) {
		return
	}
	v, err := h.Store.AIPlan(c, p.ID)
	if err != nil {
		response.Internal(c)
		return
	}
	var summary []struct {
		State string `json:"state"`
		Count int    `json:"count"`
	}
	if err := h.Store.DB.NewRaw(`SELECT b.state, count(*) FROM license_ai_bindings b JOIN licenses l ON l.id=b.license_id WHERE l.plan_id=? GROUP BY b.state`, p.ID).Scan(c, &summary); err != nil {
		response.Internal(c)
		return
	}
	response.OK(c, gin.H{"enabled": v.Config.Enabled, "group_id": v.Config.GroupID, "quota": v.Config.Quota, "rate_limit_5h": v.Config.Rate5H, "rate_limit_1d": v.Config.Rate1D, "rate_limit_7d": v.Config.Rate7D, "revision": v.Revision, "summary": summary})
}
func (h *AdminHandler) PutAIPlan(c *gin.Context) {
	if !h.aiReady(c) {
		return
	}
	p, err := h.Store.FindPlanByID(c, c.Param("id"))
	if err != nil {
		response.NotFound(c, "plan not found")
		return
	}
	if !h.aiProduct(c, p.ProductID) {
		return
	}
	var req model.AIPlanSettings
	if c.ShouldBindJSON(&req) != nil {
		response.Err(c, 400, "AI_REQUEST_INVALID", "invalid AI settings")
		return
	}
	if err := service.ValidateAIConfig(req); err != nil {
		aiError(c, err, 400)
		return
	}
	binding := ""
	validate := req
	validate.Enabled = true
	if req.GroupID <= 0 {
		var count int
		err := h.Store.DB.NewRaw("SELECT count(*) FROM license_ai_bindings b JOIN licenses l ON l.id=b.license_id WHERE l.plan_id=?", p.ID).Scan(c, &count)
		if err != nil {
			response.Internal(c)
			return
		}
		if count > 0 {
			response.Err(c, 400, "AI_GROUP_INVALID", "enrolled plans require a positive group ID")
			return
		}
	}
	if req.Enabled || req.GroupID > 0 || req.Quota > 0 {
		if err := service.ValidateAIConfig(validate); err != nil {
			aiError(c, err, 400)
			return
		}
		if p.LicenseType != "subscription" || p.GraceDays != 0 {
			response.Err(c, 400, "AI_PLAN_INELIGIBLE", "AI requires a subscription with zero grace")
			return
		}
		binding, err = service.AIProfileBinding(p)
		if err != nil {
			aiError(c, err, 400)
			return
		}
		if req.Enabled {
			if _, err = h.AI.Account(c, p.ProductID); err != nil {
				aiError(c, err, 400)
				return
			}
		}
	}
	record := &store.AIPlanRecord{Config: req, ProfileBinding: binding}
	if err := h.Store.SaveAIPlan(c, p.ID, record); err != nil {
		aiError(c, err, 500)
		return
	}
	h.Store.Audit(c, &model.AuditLog{Entity: "plan", EntityID: p.ID, Action: "ai_defaults_updated", ActorType: "admin", ActorID: adminID(c)})
	response.OK(c, gin.H{"revision": record.Revision, "affected": record.Affected})
}

func (h *AdminHandler) GetLicenseAI(c *gin.Context) {
	if !h.aiReady(c) || !h.checkLicenseScope(c, c.Param("id")) {
		return
	}
	b, err := h.Store.AIBinding(c, c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		response.OK(c, gin.H{"enrolled": false})
		return
	}
	if err != nil {
		response.Internal(c)
		return
	}
	jobs, err := h.Store.AIJobs(c, b.LicenseID)
	if err != nil {
		response.Internal(c)
		return
	}
	b.LastErrorCode = service.AIErrorCode(errorText(b.LastError))
	for _, job := range jobs {
		job.LastErrorCode = service.AIErrorCode(errorText(job.LastError))
	}
	response.OK(c, gin.H{"enrolled": true, "binding": b, "jobs": jobs, "key_hint": func() string {
		if b.RemoteID > 0 {
			return "•••• (managed)"
		}
		return ""
	}()})
}
func (h *AdminHandler) PutLicenseAI(c *gin.Context) {
	if !h.aiReady(c) || !h.checkLicenseScope(c, c.Param("id")) {
		return
	}
	var req model.AIPlanSettings
	if c.ShouldBindJSON(&req) != nil {
		response.Err(c, 400, "AI_REQUEST_INVALID", "invalid limits")
		return
	}
	req.Enabled = true
	if err := service.ValidateAIConfig(req); err != nil {
		aiError(c, err, 400)
		return
	}
	err := h.Store.DB.RunInTx(c, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT id FROM licenses WHERE id = ? FOR UPDATE", c.Param("id")); err != nil {
			return err
		}
		if deleting, err := store.AIDeleting(ctx, tx, c.Param("id")); err != nil {
			return err
		} else if deleting {
			return store.ErrAIDeletionInProgress
		}
		var b model.AIBinding
		if err := tx.NewSelect().Model(&b).Where("license_id = ?", c.Param("id")).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		b.Config = req
		b.State = "pending"
		if _, err := tx.NewUpdate().Model(&b).Column("config", "state").WherePK().Exec(ctx); err != nil {
			return err
		}
		return store.EnqueueAI(ctx, tx, b.LicenseID, "sync", "settings:"+uuid.NewString())
	})
	if err != nil {
		response.Err(c, 400, "AI_OPERATION_FAILED", "license AI configuration update failed")
		return
	}
	h.Store.Audit(c, &model.AuditLog{Entity: "license", EntityID: c.Param("id"), Action: "ai_limits_updated", ActorType: "admin", ActorID: adminID(c)})
	h.GetLicenseAI(c)
}
func (h *AdminHandler) LicenseAIAction(c *gin.Context) {
	if !h.aiReady(c) || !h.checkLicenseScope(c, c.Param("id")) {
		return
	}
	action := c.Param("action")
	if action != "reset_quota" && action != "reset_rate" && action != "rotate" && action != "retry" {
		response.Err(c, 400, "AI_REQUEST_INVALID", "unknown AI action")
		return
	}
	dedupe := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if dedupe == "" || len(dedupe) > 200 {
		response.Err(c, 400, "AI_REQUEST_INVALID", "Idempotency-Key is required")
		return
	}
	err := h.Store.DB.RunInTx(c, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT id FROM licenses WHERE id = ? FOR UPDATE", c.Param("id")); err != nil {
			return err
		}
		if deleting, err := store.AIDeleting(ctx, tx, c.Param("id")); err != nil {
			return err
		} else if deleting {
			return store.ErrAIDeletionInProgress
		}
		var b model.AIBinding
		if err := tx.NewSelect().Model(&b).Where("license_id = ?", c.Param("id")).For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		if action == "retry" {
			var job model.AIJob
			if err := tx.NewSelect().Model(&job).Where("license_id = ? AND state IN ('failed','uncertain')", b.LicenseID).OrderExpr("CASE WHEN state = 'uncertain' THEN 0 ELSE 1 END, created_at ASC").Limit(1).Scan(ctx); err != nil {
				return err
			}
			if job.Phase == "reset_sent" {
				return errors.New("reset result unknown; automatic replay refused")
			}
			_, err := tx.ExecContext(ctx, "UPDATE license_ai_jobs SET state = 'pending', last_error = '' WHERE id = ?", job.ID)
			return err
		}
		var count int
		if err := tx.NewRaw("SELECT count(*) FROM license_ai_jobs WHERE license_id = ? AND (state IN ('pending','running','uncertain') OR state = 'failed' AND (action <> 'sync' OR phase NOT IN ('','published')))", b.LicenseID).Scan(ctx, &count); err != nil {
			return err
		}
		if count > 0 {
			return errors.New("resolve pending or uncertain operation first")
		}
		return store.EnqueueAI(ctx, tx, b.LicenseID, action, action+":"+dedupe)
	})
	if err != nil {
		aiError(c, err, 400)
		return
	}
	h.Store.Audit(c, &model.AuditLog{Entity: "license", EntityID: c.Param("id"), Action: "ai_" + action, ActorType: "admin", ActorID: adminID(c)})
	h.GetLicenseAI(c)
}

func errorText(message string) error {
	if message == "" {
		return nil
	}
	return errors.New(message)
}
func aiError(c *gin.Context, err error, status int) {
	response.Err(c, status, service.AIErrorCode(err), "AI operation could not be completed")
}
