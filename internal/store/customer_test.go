package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/tabloy/keygate/internal/model"
)

func TestCustomerLifecycleAndLicenseAssociation(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")

	customer := &model.Customer{
		Kind: "organization", Name: "Acme " + suffix,
		PrimaryEmail:       "billing-" + suffix + "@example.com",
		ExternalCustomerID: "external-" + suffix,
	}
	if err := s.CreateCustomer(ctx, customer); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	license := createTestLicense(t, s, ctx)
	if err := s.SetLicenseCustomer(ctx, license.ID, customer.ID); err != nil {
		t.Fatalf("associate license: %v", err)
	}

	detail, err := s.GetCustomerDetail(ctx, customer.ID)
	if err != nil {
		t.Fatalf("customer detail: %v", err)
	}
	if len(detail.Licenses) != 1 || detail.Licenses[0].ID != license.ID {
		t.Fatalf("customer licenses = %#v, want %s", detail.Licenses, license.ID)
	}

	originalLicenseEmail := detail.Licenses[0].Email
	customer.PrimaryEmail = "new-" + suffix + "@example.com"
	if err := s.UpdateCustomer(ctx, customer); err != nil {
		t.Fatalf("update customer: %v", err)
	}
	reloaded, err := s.FindLicenseByID(ctx, license.ID)
	if err != nil {
		t.Fatalf("reload license: %v", err)
	}
	if reloaded.Email != originalLicenseEmail {
		t.Fatalf("license delivery email changed from %q to %q", originalLicenseEmail, reloaded.Email)
	}

	if err := s.SetCustomerArchived(ctx, customer.ID, true); err != nil {
		t.Fatalf("archive customer: %v", err)
	}
	archived, err := s.FindCustomerByID(ctx, customer.ID)
	if err != nil || archived.ArchivedAt == nil {
		t.Fatalf("archived customer = %#v, err = %v", archived, err)
	}
	if err := s.SetCustomerArchived(ctx, customer.ID, false); err != nil {
		t.Fatalf("restore customer: %v", err)
	}
}

func TestListUsersIncludesEveryRole(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	for _, role := range []string{model.RoleOwner, model.RoleAdmin, model.RoleUser} {
		if err := s.CreatePlaceholderUser(ctx, role+"-"+suffix+"@example.com", role); err != nil {
			t.Fatalf("create %s user: %v", role, err)
		}
	}
	users, _, err := s.ListUsers(ctx, suffix, "", 0, 20)
	if err != nil {
		t.Fatalf("list all users: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("all-role user count = %d, want 3", len(users))
	}
	admins, _, err := s.ListUsers(ctx, suffix, model.RoleAdmin, 0, 20)
	if err != nil || len(admins) != 1 || admins[0].Role != model.RoleAdmin {
		t.Fatalf("admin role filter = %#v, err = %v", admins, err)
	}
}
