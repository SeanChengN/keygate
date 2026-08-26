package handler

import "testing"

func TestSettingsMapsAreConsistent(t *testing.T) {
	for key := range settingsServerOwned {
		if settingsWritable[key] {
			t.Errorf("%q is server-owned but also writable", key)
		}
		if settingsSecret[key] {
			t.Errorf("%q is server-owned and must not be exposed as a writable secret", key)
		}
	}
	for key := range settingsSecret {
		if !settingsWritable[key] {
			t.Errorf("%q is write-only but not writable", key)
		}
	}
}

func TestGetSettingsOutputIsWritableBack(t *testing.T) {
	stored := map[string]string{
		"site_name":                  "Acme",
		"smtp_password":              "secret",
		"stripe_webhook_secret":      "whsec_live",
		"stripe_webhook_endpoint_id": "we_1",
	}

	for key, val := range stored {
		if settingsServerOwned[key] || settingsSecret[key] {
			continue
		}
		if !settingsWritable[key] {
			t.Errorf("GetSettings would return %q=%q but UpdateSettings rejects it", key, val)
		}
	}

	for _, key := range []string{"smtp_password", "stripe_webhook_secret", "stripe_webhook_endpoint_id"} {
		if !settingsSecret[key] && !settingsServerOwned[key] {
			t.Errorf("%q must never be returned by GetSettings", key)
		}
	}
}
