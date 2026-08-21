package handler

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/tabloy/keygate/internal/service"
	"github.com/tabloy/keygate/pkg/apperr"
	"github.com/tabloy/keygate/pkg/response"
)

const maxIdentifierLen = 256

type deviceProofRequest struct {
	DevicePublicKey string `json:"device_public_key"`
	ProofTimestamp  int64  `json:"proof_timestamp"`
	ProofNonce      string `json:"proof_nonce"`
	ProofSignature  string `json:"proof_signature"`
}

func (p deviceProofRequest) serviceInput() service.DeviceProofInput {
	return service.DeviceProofInput{
		PublicKey: p.DevicePublicKey, Timestamp: p.ProofTimestamp,
		Nonce: p.ProofNonce, Signature: p.ProofSignature,
	}
}

type LicenseHandler struct {
	svc *service.LicenseService
}

func NewLicenseHandler(svc *service.LicenseService) *LicenseHandler {
	return &LicenseHandler{svc: svc}
}

func (h *LicenseHandler) Activate(c *gin.Context) {
	var req struct {
		LicenseKey     string `json:"license_key" binding:"required"`
		Identifier     string `json:"identifier" binding:"required"`
		IdentifierType string `json:"identifier_type"`
		Label          string `json:"label"`
		ProductID      string `json:"product_id"`
		deviceProofRequest
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "license_key and identifier are required")
		return
	}
	req.Identifier = strings.TrimSpace(req.Identifier)
	if len(req.Identifier) > maxIdentifierLen {
		response.BadRequest(c, "identifier too long")
		return
	}

	productID, _ := c.Get("product_id")
	result, err := h.svc.Activate(c.Request.Context(), service.ActivateInput{
		LicenseKey:     req.LicenseKey,
		Identifier:     req.Identifier,
		IdentifierType: req.IdentifierType,
		Label:          req.Label,
		IPAddress:      c.ClientIP(),
		ProductID:      firstNonEmpty(str(productID), strings.TrimSpace(req.ProductID)),
		DeviceProof:    req.deviceProofRequest.serviceInput(),
	})
	if err != nil {
		writeAppErr(c, err)
		return
	}
	response.OK(c, result)
}

func (h *LicenseHandler) Verify(c *gin.Context) {
	var req struct {
		LicenseKey string `json:"license_key" binding:"required"`
		Identifier string `json:"identifier" binding:"required"`
		ProductID  string `json:"product_id"`
		deviceProofRequest
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "license_key and identifier are required")
		return
	}
	req.Identifier = strings.TrimSpace(req.Identifier)
	if len(req.Identifier) > maxIdentifierLen {
		response.BadRequest(c, "identifier too long")
		return
	}

	productID, _ := c.Get("product_id")
	result, err := h.svc.Verify(c.Request.Context(), service.VerifyInput{
		LicenseKey:  req.LicenseKey,
		Identifier:  req.Identifier,
		ProductID:   firstNonEmpty(str(productID), strings.TrimSpace(req.ProductID)),
		IPAddress:   c.ClientIP(),
		DeviceProof: req.deviceProofRequest.serviceInput(),
	})
	if err != nil {
		writeAppErr(c, err)
		return
	}
	response.OK(c, result)
}

func (h *LicenseHandler) Deactivate(c *gin.Context) {
	var req struct {
		LicenseKey string `json:"license_key" binding:"required"`
		Identifier string `json:"identifier" binding:"required"`
		ProductID  string `json:"product_id"`
		deviceProofRequest
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "license_key and identifier are required")
		return
	}
	req.Identifier = strings.TrimSpace(req.Identifier)
	if len(req.Identifier) > maxIdentifierLen {
		response.BadRequest(c, "identifier too long")
		return
	}

	productID, _ := c.Get("product_id")
	err := h.svc.Deactivate(c.Request.Context(), service.DeactivateInput{
		LicenseKey:  req.LicenseKey,
		Identifier:  req.Identifier,
		ProductID:   firstNonEmpty(str(productID), strings.TrimSpace(req.ProductID)),
		IPAddress:   c.ClientIP(),
		DeviceProof: req.deviceProofRequest.serviceInput(),
	})
	if err != nil {
		writeAppErr(c, err)
		return
	}
	response.OK(c, gin.H{"status": "deactivated"})
}

// writeAppErr translates an AppError into a consistent API response.
func writeAppErr(c *gin.Context, err error) {
	var ae *apperr.AppError
	if errors.As(err, &ae) {
		if ae.Details != nil {
			response.ErrWithDetails(c, ae.Status, ae.Code, ae.Message, ae.Details)
		} else {
			response.Err(c, ae.Status, ae.Code, ae.Message)
		}
		return
	}
	response.Internal(c)
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
