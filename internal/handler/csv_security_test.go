package handler

import "testing"

func TestCSVSafePreventsSpreadsheetFormulaInjection(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"=1+1", "'=1+1"},
		{" +cmd|' /C calc'!A0", "' +cmd|' /C calc'!A0"},
		{"\t-2+3", "'\t-2+3"},
		{"@SUM(A1:A2)", "'@SUM(A1:A2)"},
		{"ordinary text", "ordinary text"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := csvSafe(tt.input); got != tt.want {
			t.Errorf("csvSafe(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
