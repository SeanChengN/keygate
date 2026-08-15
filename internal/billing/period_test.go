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
		timezone string
		want     string
	}{
		{"ordinary month", "2026-08-11T09:30:00Z", "month", "UTC", "2026-09-11T23:59:59Z"},
		{"month end clamp", "2026-01-31T09:30:00Z", "month", "UTC", "2026-02-28T23:59:59Z"},
		{"leap month end clamp", "2028-01-31T09:30:00Z", "month", "UTC", "2028-02-29T23:59:59Z"},
		{"leap year clamp", "2028-02-29T09:30:00Z", "year", "UTC", "2029-02-28T23:59:59Z"},
		{"Shanghai example", "2026-08-15T18:00:00+08:00", "month", "Asia/Shanghai", "2026-09-15T23:59:59+08:00"},
		{"New York DST", "2026-02-08T18:00:00-05:00", "month", "America/New_York", "2026-03-08T23:59:59-04:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, err := time.Parse(time.RFC3339, tt.start)
			if err != nil {
				t.Fatal(err)
			}
			location, err := time.LoadLocation(tt.timezone)
			if err != nil {
				t.Fatal(err)
			}
			got, err := AddPeriod(start, tt.interval, location)
			if err != nil {
				t.Fatal(err)
			}
			if got.Format(time.RFC3339) != tt.want {
				t.Fatalf("got %s, want %s", got.Format(time.RFC3339), tt.want)
			}
		})
	}
	if _, err := AddPeriod(time.Now(), "week", time.UTC); err == nil {
		t.Fatal("unsupported interval accepted")
	}
}

func TestParseExpiry(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		input string
		want  string
	}{
		{"2026-09-15", "2026-09-15T23:59:59+08:00"},
		{"2026-09-15T08:00:00+08:00", "2026-09-15T23:59:59+08:00"},
		{"2026-09-15T20:00:00Z", "2026-09-16T23:59:59+08:00"},
	}
	for _, tt := range tests {
		got, err := ParseExpiry(tt.input, shanghai)
		if err != nil {
			t.Fatal(err)
		}
		if got.Format(time.RFC3339) != tt.want {
			t.Fatalf("ParseExpiry(%q) = %s, want %s", tt.input, got.Format(time.RFC3339), tt.want)
		}
	}
	if _, err := ParseExpiry("15/09/2026", shanghai); err == nil {
		t.Fatal("invalid expiry accepted")
	}
}

func TestLocation(t *testing.T) {
	location, err := Location("")
	if err != nil || location != time.UTC {
		t.Fatalf("empty timezone = %v, %v; want UTC", location, err)
	}
	location, err = Location("Asia/Shanghai")
	if err != nil || location.String() != "Asia/Shanghai" {
		t.Fatalf("Shanghai timezone = %v, %v", location, err)
	}
	if _, err := Location("Mars/Warehouse"); err == nil {
		t.Fatal("invalid IANA timezone accepted")
	}
}
