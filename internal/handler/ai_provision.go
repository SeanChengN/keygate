package handler

import (
	"database/sql"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/service"
	"github.com/tabloy/keygate/pkg/response"
	"strings"
)

func (h *AdminHandler) aiPlanScope(c *gin.Context) *model.Plan {
	if !h.aiReady(c) {
		return nil
	}
	p, err := h.Store.FindPlanByID(c, c.Param("id"))
	if err != nil {
		response.NotFound(c, "plan not found")
		return nil
	}
	if !h.aiProduct(c, p.ProductID) {
		return nil
	}
	return p
}
func aiProvisionError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrAIConfigChanged) {
		response.Err(c, 409, "AI_CONFIG_CHANGED", "AI 配置已变化，请重新预览")
	} else {
		response.Err(c, 409, "AI_PROVISION_UNAVAILABLE", err.Error())
	}
}
func (h *AdminHandler) PreviewAIProvision(c *gin.Context) {
	p := h.aiPlanScope(c)
	if p == nil {
		return
	}
	v, err := h.AI.PreviewProvision(c, p.ID)
	if err != nil {
		aiProvisionError(c, err)
		return
	}
	response.OK(c, v)
}
func (h *AdminHandler) CreateAIProvisionBatch(c *gin.Context) {
	p := h.aiPlanScope(c)
	if p == nil {
		return
	}
	var req struct {
		Revision int64    `json:"revision"`
		IDs      []string `json:"license_ids"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "invalid provisioning request")
		return
	}
	b, err := h.AI.Provision(c, p.ID, adminID(c), strings.TrimSpace(c.GetHeader("Idempotency-Key")), req.Revision, req.IDs)
	if err != nil {
		aiProvisionError(c, err)
		return
	}
	h.Store.Audit(c, &model.AuditLog{Entity: "plan", EntityID: p.ID, Action: "ai_provision_batch", ActorType: "admin", ActorID: adminID(c), Changes: map[string]any{"batch_id": b.ID, "count": len(req.IDs)}})
	response.OK(c, b)
}
func (h *AdminHandler) GetAIProvisionBatch(c *gin.Context) {
	p := h.aiPlanScope(c)
	if p == nil {
		return
	}
	b, err := h.AI.ProvisionBatch(c, p.ID, c.Param("batch"))
	if err != nil {
		response.NotFound(c, "batch not found")
		return
	}
	response.OK(c, b)
}
func (h *AdminHandler) ListAIProvisionBatches(c *gin.Context) {
	p := h.aiPlanScope(c)
	if p == nil {
		return
	}
	batches := []*service.AIProvisionBatch{}
	if err := h.Store.DB.NewSelect().Model(&batches).Where("plan_id=?", p.ID).Order("created_at DESC").Limit(10).Scan(c); err != nil {
		response.Internal(c)
		return
	}
	response.OK(c, gin.H{"batches": batches})
}
func (h *AdminHandler) ProvisionLicenseAI(c *gin.Context) {
	if !h.aiReady(c) || !h.checkLicenseScope(c, c.Param("id")) {
		return
	}
	l, err := h.Store.FindLicenseByID(c, c.Param("id"))
	if err != nil {
		response.NotFound(c, "license not found")
		return
	}
	if !h.aiProduct(c, l.ProductID) {
		return
	}
	if b, e := h.Store.AIBinding(c, l.ID); e == nil {
		response.OK(c, gin.H{"enrolled": true, "state": b.State})
		return
	} else if !errors.Is(e, sql.ErrNoRows) {
		response.Internal(c)
		return
	}
	cfg, err := h.AI.ProvisionConfig(c, l.PlanID)
	if err != nil {
		aiProvisionError(c, err)
		return
	}
	if !service.AIProvisionEligible(l) {
		response.Err(c, 409, "AI_LICENSE_UNAVAILABLE", "许可证当前不可用，请先恢复或续期后再补配")
		return
	}
	b, err := h.AI.Provision(c, l.PlanID, adminID(c), strings.TrimSpace(c.GetHeader("Idempotency-Key")), cfg.Revision, []string{l.ID})
	if err != nil {
		aiProvisionError(c, err)
		return
	}
	h.Store.Audit(c, &model.AuditLog{Entity: "license", EntityID: l.ID, Action: "ai_provision_manual", ActorType: "admin", ActorID: adminID(c)})
	response.OK(c, b)
}
