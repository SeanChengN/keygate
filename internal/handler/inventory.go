package handler

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
	"github.com/tabloy/keygate/pkg/response"
	"github.com/uptrace/bun"
)

// These explicit credential-export endpoints are never available to API keys.
func inventoryAdmin(c *gin.Context) bool {
	if _, exists := c.Get("api_key"); exists {
		response.Err(c, 403, "INVENTORY_ADMIN_REQUIRED", "administrator session required")
		return false
	}
	return true
}

type inventoryLicenseMetadata struct {
	ID            string    `json:"id"`
	Status        string    `json:"status"`
	FirstExportID string    `json:"first_export_id"`
	CreatedAt     time.Time `json:"created_at"`
}

func inventoryError(c *gin.Context, err error) {
	if errors.Is(err, store.ErrInventoryConflict) {
		response.Err(c, 409, "INVENTORY_CONFLICT", "请求已变化，请核对原批次或重新选择")
		return
	}
	if errors.Is(err, store.ErrInventoryIneligible) {
		response.Err(c, 409, "INVENTORY_INELIGIBLE", err.Error())
		return
	}
	response.Internal(c)
}
func (h *AdminHandler) CreateInventoryBatch(c *gin.Context) {
	if !inventoryAdmin(c) {
		return
	}
	c.Header("Cache-Control", "no-store")
	var req struct {
		ProductID string `json:"product_id"`
		PlanID    string `json:"plan_id"`
		Quantity  int    `json:"quantity"`
		Notes     string `json:"notes"`
	}
	key := c.GetHeader("Idempotency-Key")
	if c.ShouldBindJSON(&req) != nil || !store.ValidIdempotencyKey(key) || req.Quantity < 1 || req.Quantity > 500 || len(req.Notes) > 2000 {
		response.Err(c, 400, "INVENTORY_REQUEST_INVALID", "需要产品、方案、1–500 张数量及 Idempotency-Key")
		return
	}
	if !h.aiReady(c) || !h.aiProduct(c, req.ProductID) {
		return
	}
	p, err := h.Store.FindPlanByID(c, req.PlanID)
	if err != nil || p.ProductID != req.ProductID {
		response.Err(c, 400, "INVENTORY_PLAN_MISMATCH", "方案与产品不匹配")
		return
	}
	cfg, err := h.AI.ProvisionConfig(c, p.ID)
	if err != nil {
		aiError(c, err, 400)
		return
	}
	metadata, err := h.AI.AccountMetadata(c, req.ProductID)
	if err != nil {
		aiError(c, err, 500)
		return
	}
	if !metadata.Configured {
		aiError(c, errors.New("jiji account is not configured"), 409)
		return
	}
	if !metadata.Enabled || metadata.State != "ready" {
		aiError(c, errors.New("jiji account needs administrator attention"), 409)
		return
	}
	b := &store.InventoryBatch{ProductID: req.ProductID, PlanID: req.PlanID, ActorID: adminID(c), RequestKey: key, Quantity: req.Quantity, Notes: req.Notes}
	if err = h.Store.CreateInventory(c, b, cfg); err != nil {
		inventoryError(c, err)
		return
	}
	response.Created(c, b)
}
func (h *AdminHandler) GetInventoryBatch(c *gin.Context) {
	if !inventoryAdmin(c) {
		return
	}
	c.Header("Cache-Control", "no-store")
	b, err := h.Store.InventoryBatch(c, c.Param("id"))
	if err != nil {
		response.NotFound(c, "batch not found")
		return
	}
	if !requireKeyProductScope(c, b.ProductID) {
		return
	}
	var rows []inventoryLicenseMetadata
	if err = h.Store.DB.NewSelect().Table("licenses").Column("id", "status", "first_export_id", "created_at").Where("issuance_batch_id=?", b.ID).Order("id").Scan(c, &rows); err != nil {
		response.Internal(c)
		return
	}
	response.OK(c, gin.H{"batch": b, "licenses": rows})
}
func (h *AdminHandler) ListInventoryBatches(c *gin.Context) {
	if !inventoryAdmin(c) {
		return
	}
	c.Header("Cache-Control", "no-store")
	var rows []store.InventoryBatch
	q := h.Store.DB.NewSelect().Model(&rows).OrderExpr("created_at DESC").Limit(50)
	if id := c.Query("product_id"); id != "" {
		if !requireKeyProductScope(c, id) {
			return
		}
		q = q.Where("product_id=?", id)
	}
	if err := q.Scan(c); err != nil {
		response.Internal(c)
		return
	}
	response.OK(c, gin.H{"batches": rows})
}
func (h *AdminHandler) CreateKeyExport(c *gin.Context) {
	if !inventoryAdmin(c) {
		return
	}
	c.Header("Cache-Control", "no-store")
	var req struct {
		LicenseIDs []string `json:"license_ids"`
	}
	key := c.GetHeader("Idempotency-Key")
	if c.ShouldBindJSON(&req) != nil || !store.ValidIdempotencyKey(key) {
		response.Err(c, 400, "INVENTORY_REQUEST_INVALID", "license_ids and Idempotency-Key required")
		return
	}
	for _, id := range req.LicenseIDs {
		if !h.checkLicenseScope(c, id) {
			return
		}
	}
	e := &store.KeyExport{ActorID: adminID(c), RequestKey: key, LicenseIDs: req.LicenseIDs}
	if err := h.Store.CreateKeyExport(c, e); err != nil {
		inventoryError(c, err)
		return
	}
	response.Created(c, e)
}
func (h *AdminHandler) ListKeyExports(c *gin.Context) {
	if !inventoryAdmin(c) {
		return
	}
	c.Header("Cache-Control", "no-store")
	var rows []store.KeyExport
	if err := h.Store.DB.NewSelect().Model(&rows).OrderExpr("created_at DESC").Limit(50).Scan(c); err != nil {
		response.Internal(c)
		return
	}
	response.OK(c, gin.H{"exports": rows})
}
func (h *AdminHandler) DownloadKeyExport(c *gin.Context) {
	if !inventoryAdmin(c) {
		return
	}
	c.Header("Cache-Control", "no-store")
	e, err := h.Store.ExportRecord(c, c.Param("id"))
	if err != nil {
		response.NotFound(c, "export not found")
		return
	}
	var rows []*model.License
	if err = h.Store.DB.NewSelect().Model(&rows).Where("id IN (?)", bun.In(e.LicenseIDs)).Order("id").Scan(c); err != nil {
		response.Internal(c)
		return
	}
	if len(rows) != len(e.LicenseIDs) {
		response.Err(c, 409, "EXPORT_RECORD_MISSING", "原清单部分许可证已删除，不能生成缺行文件")
		return
	}
	keys := make([]string, 0, len(rows))
	for _, l := range rows {
		if !h.checkLicenseScope(c, l.ID) {
			return
		}
		key := h.Store.DecryptLicenseKey(l)
		if key == "" {
			response.Err(c, 503, "LICENSE_KEY_UNAVAILABLE", "许可证无法解密")
			return
		}
		keys = append(keys, key)
	}
	h.Store.Audit(c, &model.AuditLog{Entity: "license_export", EntityID: e.ID, Action: "downloaded", ActorID: adminID(c), ActorType: "admin", Changes: map[string]any{"count": len(rows)}})
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="keygate-licenses-%s.txt"`, e.ID))
	c.Data(200, "text/plain; charset=utf-8", []byte(strings.Join(keys, "\n")+"\n"))
}
