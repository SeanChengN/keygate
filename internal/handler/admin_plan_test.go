package handler

import "testing"

func TestValidateBillingInterval(t *testing.T) {
	tests := []struct {
		name        string
		licenseType string
		interval    string
		wantErr     bool
	}{
		{name: "monthly subscription", licenseType: "subscription", interval: "month"},
		{name: "yearly subscription", licenseType: "subscription", interval: "year"},
		{name: "subscription without interval", licenseType: "subscription", wantErr: true},
		{name: "invalid subscription interval", licenseType: "subscription", interval: "week", wantErr: true},
		{name: "perpetual", licenseType: "perpetual"},
		{name: "trial", licenseType: "trial"},
		{name: "perpetual with interval", licenseType: "perpetual", interval: "month", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBillingInterval(tt.licenseType, tt.interval)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateBillingInterval(%q, %q) error = %v, wantErr=%v", tt.licenseType, tt.interval, err, tt.wantErr)
			}
		})
	}
}
