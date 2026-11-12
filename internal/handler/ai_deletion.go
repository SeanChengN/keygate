package handler

import (
	"database/sql"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/pkg/response"
)

func deletionView(d *model.AIDeletion) gin.H {
	done := 0
	for _, target := range d.Targets {
		if target.Phase == "absent" {
			done++
		}
	}
	return gin.H{"id": d.ID, "license_id": d.LicenseID, "state": d.State, "last_error_code": d.LastErrorCode, "completed_targets": done, "total_targets": len(d.Targets)}
}
func (h *AdminHandler) GetLicenseDeletion(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	d, err := h.Store.AIDeletion(c, c.Param("id"))
	if errors.Is(err, sql.ErrNoRows) {
		if !h.checkLicenseScope(c, c.Param("id")) {
			return
		}
		response.OK(c, gin.H{"state": "none"})
		return
	}
	if err != nil {
		response.Internal(c)
		return
	}
	if !requireKeyProductScope(c, d.ProductID) {
		return
	}
	response.OK(c, deletionView(d))
}
