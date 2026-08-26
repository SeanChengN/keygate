package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tabloy/keygate/internal/model"
)

func TestLicenseResponsesRedactKeyByDefault(t *testing.T) {
	lic := &model.License{ID: "lic-1", LicenseKey: "KG-secret-value"}
	raw, err := json.Marshal(lic)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "license_key") || strings.Contains(string(raw), "KG-secret-value") {
		t.Fatalf("ordinary license response leaked key: %s", raw)
	}
}

func TestLicenseResponsesOptInToKeyOrHint(t *testing.T) {
	lic := &model.License{ID: "lic-1", LicenseKey: "KG-secret-value"}
	created, err := json.Marshal(licenseWithKey{License: lic, LicenseKey: lic.LicenseKey})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(created), `"license_key":"KG-secret-value"`) {
		t.Fatalf("creation response did not explicitly include key: %s", created)
	}

	detail, err := json.Marshal(licenseWithHint{License: lic, LicenseKeyHint: "alue"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(detail), `"license_key_hint":"alue"`) {
		t.Fatalf("detail response did not include hint: %s", detail)
	}
	if strings.Contains(string(detail), "KG-secret-value") {
		t.Fatalf("detail response leaked full key: %s", detail)
	}
}
