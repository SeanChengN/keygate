package billing

import (
	"testing"
	"time"
)

func TestAddPeriod(t *testing.T) {
	tests := []struct {
		name     string
		start    string
		interval string
		want     string
	}{
		{"ordinary month", "2026-08-11T09:30:00Z", "month", "2026-09-11T09:30:00Z"},
		{"month end clamp", "2026-01-31T09:30:00Z", "month", "2026-02-28T09:30:00Z"},
		{"leap month end clamp", "2028-01-31T09:30:00Z", "month", "2028-02-29T09:30:00Z"},
		{"leap year clamp", "2028-02-29T09:30:00Z", "year", "2029-02-28T09:30:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, err := time.Parse(time.RFC3339, tt.start)
			if err != nil {
				t.Fatal(err)
			}
			got, err := AddPeriod(start, tt.interval)
			if err != nil {
				t.Fatal(err)
			}
			if got.Format(time.RFC3339) != tt.want {
				t.Fatalf("got %s, want %s", got.Format(time.RFC3339), tt.want)
			}
		})
	}
	if _, err := AddPeriod(time.Now(), "week"); err == nil {
		t.Fatal("unsupported interval accepted")
	}
}
