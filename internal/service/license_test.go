package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	licensepkg "github.com/tabloy/keygate/internal/license"
	"github.com/tabloy/keygate/internal/model"
)

func TestSignTokenAtSeparatesCommercialExpiryFromLease(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	svc := &LicenseService{signingKey: privateKey}
	now := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	commercialEnd := time.Date(2026, time.September, 11, 8, 0, 0, 0, time.UTC)
	lic := &model.License{
		ID: "lic-1", ProductID: "product-1", PlanID: "plan-1", Status: model.StatusActive,
		ValidUntil: &commercialEnd,
		Plan:       &model.Plan{Name: "WMS 月卡", LicenseType: "subscription", BillingInterval: "month", GraceDays: 7},
	}

	signed, err := svc.signTokenAt(lic, "device-1", now)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	parsed, err := licensepkg.Verify(signed, privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if parsed.Version != 2 || parsed.PlanName != "WMS 月卡" || parsed.LicenseType != "subscription" {
		t.Fatalf("commercial identity fields mismatch: %+v", parsed)
	}
	if parsed.ValidUntil == nil || *parsed.ValidUntil != commercialEnd.Unix() {
		t.Fatalf("commercial expiry = %v, want %d", parsed.ValidUntil, commercialEnd.Unix())
	}
	if parsed.ExpiresAt != now.Add(7*24*time.Hour).Unix() {
		t.Fatalf("lease expiry = %d, want %d", parsed.ExpiresAt, now.Add(7*24*time.Hour).Unix())
	}
	if parsed.GraceDays != 7 {
		t.Fatalf("commercial grace = %d, want 7", parsed.GraceDays)
	}
}

func TestAssertUsable(t *testing.T) {
	svc := &LicenseService{}

	now := time.Now()
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)
	wayPast := now.Add(-30 * 24 * time.Hour)

	tests := []struct {
		name    string
		license *model.License
		wantErr bool
		errCode string
	}{
		{
			name:    "active license with future expiry",
			license: &model.License{Status: model.StatusActive, ValidUntil: &future, Plan: &model.Plan{GraceDays: 7}},
			wantErr: false,
		},
		{
			name:    "active license no expiry",
			license: &model.License{Status: model.StatusActive, Plan: &model.Plan{GraceDays: 7}},
			wantErr: false,
		},
		{
			name:    "active license recently expired within grace",
			license: &model.License{Status: model.StatusActive, ValidUntil: &past, Plan: &model.Plan{GraceDays: 7}},
			wantErr: false,
		},
		{
			name:    "active license expired beyond grace",
			license: &model.License{Status: model.StatusActive, ValidUntil: &wayPast, Plan: &model.Plan{GraceDays: 7}},
			wantErr: true,
			errCode: "LICENSE_EXPIRED",
		},
		{
			name:    "trialing license",
			license: &model.License{Status: model.StatusTrialing, Plan: &model.Plan{GraceDays: 7}},
			wantErr: false,
		},
		{
			name:    "canceled license within paid period",
			license: &model.License{Status: model.StatusCanceled, ValidUntil: &future},
			wantErr: false,
		},
		{
			name:    "canceled license past paid period",
			license: &model.License{Status: model.StatusCanceled, ValidUntil: &past},
			wantErr: true,
			errCode: "LICENSE_CANCELED",
		},
		{
			name:    "suspended license",
			license: &model.License{Status: model.StatusSuspended},
			wantErr: true,
			errCode: "LICENSE_SUSPENDED",
		},
		{
			name:    "revoked license",
			license: &model.License{Status: model.StatusRevoked},
			wantErr: true,
			errCode: "LICENSE_REVOKED",
		},
		{
			name:    "expired license",
			license: &model.License{Status: model.StatusExpired},
			wantErr: true,
			errCode: "LICENSE_EXPIRED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.assertUsable(tt.license)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				// Check error contains expected code
				if tt.errCode != "" && !containsCode(err, tt.errCode) {
					t.Fatalf("expected error code %s, got %v", tt.errCode, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}
		})
	}
}

func containsCode(err error, code string) bool {
	return err != nil && containsStr(err.Error(), code)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestMaxActivations(t *testing.T) {
	svc := &LicenseService{}

	tests := []struct {
		name string
		lic  *model.License
		want int
	}{
		{"with plan", &model.License{Plan: &model.Plan{MaxActivations: 5}}, 5},
		{"without plan", &model.License{}, 3},
		{"nil plan", &model.License{Plan: nil}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.maxActivations(tt.lic)
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGraceDays(t *testing.T) {
	svc := &LicenseService{}

	tests := []struct {
		name string
		lic  *model.License
		want int
	}{
		{"with plan", &model.License{Plan: &model.Plan{GraceDays: 14}}, 14},
		{"without plan", &model.License{}, 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.graceDays(tt.lic)
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEntitlements(t *testing.T) {
	svc := &LicenseService{}

	lic := &model.License{
		Plan: &model.Plan{
			Entitlements: []*model.Entitlement{
				{Feature: "export", ValueType: "bool", Value: "true"},
				{Feature: "sso", ValueType: "bool", Value: "false"},
				{Feature: "max_users", ValueType: "int", Value: "50"},
				{Feature: "sla", ValueType: "string", Value: "99.9%"},
			},
		},
	}

	features := svc.entitlements(lic)

	if features["export"] != true {
		t.Error("export should be true")
	}
	if features["sso"] != false {
		t.Error("sso should be false")
	}
	if features["max_users"] != "50" {
		t.Errorf("max_users should be '50', got %v", features["max_users"])
	}
	if features["sla"] != "99.9%" {
		t.Errorf("sla should be '99.9%%', got %v", features["sla"])
	}

	nilLic := &model.License{}
	emptyFeatures := svc.entitlements(nilLic)
	if len(emptyFeatures) != 0 {
		t.Error("nil plan should return empty features")
	}
}
