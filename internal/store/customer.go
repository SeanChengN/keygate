package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/tabloy/keygate/internal/model"
	"github.com/uptrace/bun"
)

var ErrCustomerIdentifierConflict = errors.New("customer identifier already exists")

type CustomerListFilter struct {
	Search string
	Kind   string
	Status string
	Offset int
	Limit  int
}

type CustomerDetail struct {
	Customer      *model.Customer       `json:"customer"`
	Licenses      []*model.License      `json:"licenses"`
	Subscriptions []*model.Subscription `json:"subscriptions"`
	TotalUsage    int64                 `json:"total_usage"`
	ActiveSeats   int                   `json:"active_seats"`
	Activations   int                   `json:"activations"`
}

func (s *Store) ListCustomers(ctx context.Context, f CustomerListFilter) ([]*model.Customer, int, error) {
	q := s.DB.NewSelect().Model((*model.Customer)(nil))
	if f.Search != "" {
		like := "%" + f.Search + "%"
		q = q.Where("(customer.name ILIKE ? OR customer.primary_email ILIKE ? OR customer.phone ILIKE ? OR customer.company ILIKE ? OR customer.external_customer_id ILIKE ?)", like, like, like, like, like)
	}
	if f.Kind != "" {
		q = q.Where("customer.kind = ?", f.Kind)
	}
	switch f.Status {
	case "archived":
		q = q.Where("customer.archived_at IS NOT NULL")
	case "all":
	default:
		q = q.Where("customer.archived_at IS NULL")
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	var out []*model.Customer
	err = q.OrderExpr("customer.created_at DESC").Offset(f.Offset).Limit(f.Limit).Scan(ctx, &out)
	return out, total, err
}

func (s *Store) CreateCustomer(ctx context.Context, customer *model.Customer) error {
	if customer.ID == "" {
		customer.ID = newID()
	}
	_, err := s.DB.NewInsert().Model(customer).Exec(ctx)
	if isUniqueViolation(err) {
		return ErrCustomerIdentifierConflict
	}
	return err
}

func (s *Store) FindCustomerByID(ctx context.Context, id string) (*model.Customer, error) {
	customer := new(model.Customer)
	return customer, s.DB.NewSelect().Model(customer).Where("customer.id = ?", id).Scan(ctx)
}

func (s *Store) FindCustomerByStripeID(ctx context.Context, stripeID string) (*model.Customer, error) {
	customer := new(model.Customer)
	return customer, s.DB.NewSelect().Model(customer).Where("customer.stripe_customer_id = ?", stripeID).Scan(ctx)
}

func (s *Store) UpdateCustomer(ctx context.Context, customer *model.Customer) error {
	customer.UpdatedAt = time.Now()
	_, err := s.DB.NewUpdate().Model(customer).
		Column("kind", "name", "primary_email", "phone", "company", "notes", "external_customer_id", "stripe_customer_id", "updated_at").
		WherePK().Exec(ctx)
	if isUniqueViolation(err) {
		return ErrCustomerIdentifierConflict
	}
	return err
}

func (s *Store) SetCustomerArchived(ctx context.Context, id string, archived bool) error {
	q := s.DB.NewUpdate().Model((*model.Customer)(nil)).Set("updated_at = now()").Where("id = ?", id)
	if archived {
		q = q.Set("archived_at = now()")
	} else {
		q = q.Set("archived_at = NULL")
	}
	res, err := q.Exec(ctx)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetCustomerDetail(ctx context.Context, id string) (*CustomerDetail, error) {
	customer, err := s.FindCustomerByID(ctx, id)
	if err != nil {
		return nil, err
	}
	detail := &CustomerDetail{Customer: customer, Licenses: []*model.License{}, Subscriptions: []*model.Subscription{}}
	if err := s.DB.NewSelect().Model(&detail.Licenses).
		Relation("Plan").Relation("Product").
		Where("license.customer_id = ?", id).
		OrderExpr("license.created_at DESC").Scan(ctx); err != nil {
		return nil, err
	}
	if len(detail.Licenses) == 0 {
		return detail, nil
	}
	ids := make([]string, len(detail.Licenses))
	for i, license := range detail.Licenses {
		ids[i] = license.ID
	}
	_ = s.DB.NewSelect().Model(&detail.Subscriptions).
		Relation("Plan").Where("subscription.license_id IN (?)", bun.In(ids)).
		OrderExpr("subscription.created_at DESC").Scan(ctx)
	var usage struct {
		Total int64 `bun:"total"`
	}
	_ = s.DB.NewSelect().TableExpr("usage_events").ColumnExpr("COALESCE(SUM(quantity), 0) AS total").Where("license_id IN (?)", bun.In(ids)).Scan(ctx, &usage)
	detail.TotalUsage = usage.Total
	detail.Activations, _ = s.DB.NewSelect().TableExpr("activations").Where("license_id IN (?)", bun.In(ids)).Count(ctx)
	detail.ActiveSeats, _ = s.DB.NewSelect().TableExpr("seats").Where("license_id IN (?)", bun.In(ids)).Where("removed_at IS NULL").Count(ctx)
	return detail, nil
}

// FindOrCreateStripeCustomer gives payment fulfillment a stable business
// customer without conflating it with a portal login identity.
func (s *Store) FindOrCreateStripeCustomer(ctx context.Context, stripeID, email string) (*model.Customer, bool, bool, error) {
	if stripeID == "" {
		return nil, false, false, nil
	}
	if customer, err := s.FindCustomerByStripeID(ctx, stripeID); err == nil {
		restored := customer.ArchivedAt != nil
		if customer.ArchivedAt != nil {
			if err := s.SetCustomerArchived(ctx, customer.ID, false); err != nil {
				return nil, false, false, err
			}
			customer.ArchivedAt = nil
		}
		return customer, false, restored, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, false, err
	}
	name := strings.Split(email, "@")[0]
	if name == "" {
		name = "Stripe customer"
	}
	customer := &model.Customer{Kind: "individual", Name: name, PrimaryEmail: strings.ToLower(strings.TrimSpace(email)), StripeCustomerID: stripeID}
	if err := s.CreateCustomer(ctx, customer); err != nil {
		if errors.Is(err, ErrCustomerIdentifierConflict) {
			existing, findErr := s.FindCustomerByStripeID(ctx, stripeID)
			return existing, false, false, findErr
		}
		return nil, false, false, err
	}
	return customer, true, false, nil
}
