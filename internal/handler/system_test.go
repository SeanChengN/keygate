package handler

import "testing"

func TestSemverNewer(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		// Basic comparisons
		{"1.0.1", "1.0.0", true},
		{"1.1.0", "1.0.0", true},
		{"2.0.0", "1.0.0", true},
		{"1.0.0", "1.0.0", false}, // same version
		{"1.0.0", "1.0.1", false}, // current is newer
		{"1.0.0", "2.0.0", false},

		// Multi-digit
		{"1.10.0", "1.9.0", true},
		{"1.0.10", "1.0.9", true},
		{"10.0.0", "9.0.0", true},

		// Pre-release suffix stripped
		{"1.1.0-beta", "1.0.0", true},
		{"1.0.0", "1.0.0-beta", false}, // both parse to 1.0.0

		// Edge cases
		{"", "1.0.0", false},
		{"1.0.0", "", false},
		{"1.0.0", "dev", false},
		{"", "", false},

		// Partial versions
		{"1.1", "1.0", true},
		{"1", "0", true},
	}

	for _, tt := range tests {
		got := semverNewer(tt.latest, tt.current)
		if got != tt.want {
			t.Errorf("semverNewer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}

func TestStripV(t *testing.T) {
	tests := []struct{ in, want string }{
		{"v1.0.0", "1.0.0"},
		{"1.0.0", "1.0.0"},
		{"v", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := stripV(tt.in); got != tt.want {
			t.Errorf("stripV(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSemverNewerForkReleases(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"0.1.2-dw.11", "0.1.2-dw.10", true},
		{"0.1.2-dw.10", "0.1.2-dw.11", false},
		{"0.1.3-dw.1", "0.1.2-dw.99", true},
		{"0.1.2-dw.99", "0.1.3-dw.1", false},
		{"0.1.3-dw.1", "0.1.3", true},
	}
	for _, tt := range tests {
		if got := semverNewer(tt.latest, tt.current); got != tt.want {
			t.Errorf("semverNewer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}

func TestNewSystemHandlerUsesForkRepository(t *testing.T) {
	h := NewSystemHandler(nil)
	if h.RepoOwner != "SeanChengN" || h.RepoName != "keygate" {
		t.Fatalf("update repository = %s/%s, want SeanChengN/keygate", h.RepoOwner, h.RepoName)
	}
}
