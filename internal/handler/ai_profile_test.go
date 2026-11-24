package handler

import (
	"strings"
	"testing"

	"github.com/tabloy/keygate/internal/model"
)

func TestAIProfileTiers(t *testing.T) {
	const source = `{"version":2,"provider_id":"jiji","models":{"text":["luna"],"vision":["luna"]},"defaults":{"text":"luna","vision":"luna"}}`
	const candidate = `{"version":3,"provider_id":"jiji","models":{"text":["astra","sol","luna"],"vision":["astra","terra","luna"]},"defaults":{"text":"sol","vision":"terra"}}`
	for _, test := range []struct {
		name  string
		value string
		valid bool
	}{
		{"preserves signed contract", candidate, true},
		{"no version bump", strings.Replace(candidate, ":3", ":2", 1), false},
		{"duplicate within tier group", strings.Replace(candidate, `"astra","sol","luna"`, `"luna","sol","luna"`, 1), false},
		{"unknown default", strings.Replace(candidate, `"text":"sol"`, `"text":"terra"`, 1), false},
		{"route change", strings.Replace(candidate, "jiji", "changed", 1), false},
		{"missing tier", strings.Replace(candidate, `"astra","sol","luna"`, `"sol","luna"`, 1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			old := &model.Entitlement{Value: source, ValueType: "string"}
			next := &model.Entitlement{Value: test.value, ValueType: "string"}
			if err := validateProfileChange(old, next, &old.Value); (err == nil) != test.valid {
				t.Fatalf("valid=%t, got %v", test.valid, err)
			}
		})
	}
}
