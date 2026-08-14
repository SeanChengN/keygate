package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
	"github.com/tabloy/keygate/pkg/apperr"
	"github.com/tabloy/keygate/pkg/response"
)

type customerInput struct {
	Kind               string `json:"kind"`
	Name               string `json:"name"`
	PrimaryEmail       string `json:"primary_email"`
	Phone              string `json:"phone"`
	Company            string `json:"company"`
	Notes              string `json:"notes"`
	ExternalCustomerID string `json:"external_customer_id"`
	StripeCustomerID   string `json:"stripe_customer_id"`
}

func normalizeCustomerInput(req *customerInput) {
	req.Kind = strings.TrimSpace(req.Kind)
	if req.Kind == "" {
		req.Kind = "individual"
	}
	req.Name = strings.TrimSpace(req.Name)
	req.PrimaryEmail = strings.ToLower(strings.TrimSpace(req.PrimaryEmail))
	req.Phone = strings.TrimSpace(req.Phone)
	req.Company = strings.TrimSpace(req.Company)
	req.Notes = strings.TrimSpace(req.Notes)
	req.ExternalCustomerID = strings.TrimSpace(req.ExternalCustomerID)
	req.StripeCustomerID = strings.TrimSpace(req.StripeCustomerID)
}

func validateCustomerInput(req *customerInput) *apperr.AppError {
	if req.Kind != "individual" && req.Kind != "organization" {
		return apperr.BadRequest("kind must be individual or organization")
	}
	if err := apperr.ValidateName("name", req.Name); err != nil {
		return err
	}
	if err := apperr.ValidateEmail(req.PrimaryEmail); err != nil {
		return err
	}
	if len(req.Phone) > 50 || len(req.Company) > 200 || len(req.Notes) > 4000 || len(req.ExternalCustomerID) > 256 || len(req.StripeCustomerID) > 256 {
		return apperr.BadRequest("one or more customer fields exceed their maximum length")
	}
	return nil
}

func (h *AdminHandler) ListCustomers(c *gin.Context) {
	kind, status := c.Query("kind"), c.Query("status")
	if kind != "" && kind != "individual" && kind != "organization" {
		response.BadRequest(c, "kind must be individual or organization")
		return
	}
	if status != "" && status != "active" && status != "archived" && status != "all" {
		response.BadRequest(c, "status must be active, archived, or all")
		return
	}
	limit := queryInt(c, "limit", 30)
	if limit < 1 || limit > 100 {
		limit = 30
	}
	customers, total, err := h.Store.ListCustomers(c, store.CustomerListFilter{
		Search: c.Query("search"), Kind: kind, Status: status,
		Offset: queryInt(c, "offset", 0), Limit: limit,
	})
	if err != nil {
		response.Internal(c)
		return
	}
	response.OK(c, gin.H{"customers": customers, "total": total})
}

func (h *AdminHandler) CreateCustomer(c *gin.Context) {
	var req customerInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid customer")
		return
	}
	normalizeCustomerInput(&req)
	if appErr := validateCustomerInput(&req); appErr != nil {
		response.Err(c, appErr.Status, appErr.Code, appErr.Message)
		return
	}
	customer := &model.Customer{Kind: req.Kind, Name: req.Name, PrimaryEmail: req.PrimaryEmail, Phone: req.Phone, Company: req.Company, Notes: req.Notes, ExternalCustomerID: req.ExternalCustomerID, StripeCustomerID: req.StripeCustomerID}
	if err := h.Store.CreateCustomer(c, customer); err != nil {
		if errors.Is(err, store.ErrCustomerIdentifierConflict) {
			response.Err(c, http.StatusConflict, "DUPLICATE_CUSTOMER_ID", "external or Stripe customer ID is already in use")
			return
		}
		response.Internal(c)
		return
	}
	h.Store.Audit(c, &model.AuditLog{Entity: "customer", EntityID: customer.ID, Action: "created", ActorType: "admin", ActorID: adminID(c)})
	response.Created(c, customer)
}

func (h *AdminHandler) GetCustomer(c *gin.Context) {
	detail, err := h.Store.GetCustomerDetail(c, c.Param("id"))
	if err != nil {
		response.NotFound(c, "customer not found")
		return
	}
	response.OK(c, detail)
}

func (h *AdminHandler) UpdateCustomer(c *gin.Context) {
	customer, err := h.Store.FindCustomerByID(c, c.Param("id"))
	if err != nil {
		response.NotFound(c, "customer not found")
		return
	}
	var req customerInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid customer")
		return
	}
	normalizeCustomerInput(&req)
	if appErr := validateCustomerInput(&req); appErr != nil {
		response.Err(c, appErr.Status, appErr.Code, appErr.Message)
		return
	}
	previousEmail := customer.PrimaryEmail
	customer.Kind, customer.Name, customer.PrimaryEmail = req.Kind, req.Name, req.PrimaryEmail
	customer.Phone, customer.Company, customer.Notes = req.Phone, req.Company, req.Notes
	customer.ExternalCustomerID, customer.StripeCustomerID = req.ExternalCustomerID, req.StripeCustomerID
	if err := h.Store.UpdateCustomer(c, customer); err != nil {
		if errors.Is(err, store.ErrCustomerIdentifierConflict) {
			response.Err(c, http.StatusConflict, "DUPLICATE_CUSTOMER_ID", "external or Stripe customer ID is already in use")
			return
		}
		response.Internal(c)
		return
	}
	h.Store.Audit(c, &model.AuditLog{Entity: "customer", EntityID: customer.ID, Action: "updated", ActorType: "admin", ActorID: adminID(c), Changes: map[string]any{"previous_email": previousEmail, "primary_email": customer.PrimaryEmail}})
	response.OK(c, customer)
}

func (h *AdminHandler) setCustomerArchived(c *gin.Context, archived bool) {
	id := c.Param("id")
	if err := h.Store.SetCustomerArchived(c, id, archived); err != nil {
		response.NotFound(c, "customer not found")
		return
	}
	action := "restored"
	if archived {
		action = "archived"
	}
	h.Store.Audit(c, &model.AuditLog{Entity: "customer", EntityID: id, Action: action, ActorType: "admin", ActorID: adminID(c)})
	response.OK(c, gin.H{"status": action})
}

func (h *AdminHandler) ArchiveCustomer(c *gin.Context) { h.setCustomerArchived(c, true) }
func (h *AdminHandler) RestoreCustomer(c *gin.Context) { h.setCustomerArchived(c, false) }
