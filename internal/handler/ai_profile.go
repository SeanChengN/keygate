package handler

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"

	"github.com/tabloy/keygate/internal/model"
)

type profileModels struct {
	Version  int                 `json:"version"`
	Models   map[string][]string `json:"models"`
	Defaults map[string]string   `json:"defaults"`
}

func validateProfileValue(raw string) error {
	var profile struct {
		profileModels
		ProviderID   string `json:"provider_id"`
		Name         string `json:"name"`
		BaseURL      string `json:"base_url"`
		Protocol     string `json:"protocol"`
		QuotaAdapter string `json:"quota_adapter"`
		Support      string `json:"support"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > 32*1024 || !json.Valid([]byte(raw)) || decoder.Decode(&profile) != nil ||
		profile.Version < 1 || len([]rune(profile.Name)) == 0 || len([]rune(profile.Name)) > 80 ||
		(profile.QuotaAdapter != "jiji" && profile.QuotaAdapter != "none") || len([]rune(profile.Support)) > 500 {
		return errors.New("AI profile must contain a version, service name, models, defaults and a valid quota adapter")
	}
	return validateProfileModels(profile.profileModels, false)
}

func validateProfileChange(previous, next *model.Entitlement, expected *string) error {
	if previous.Value == next.Value {
		return nil
	}
	var oldProfile, newProfile profileModels
	_ = json.Unmarshal([]byte(previous.Value), &oldProfile)
	if next.ValueType != "string" || len(next.Value) > 32*1024 ||
		json.Unmarshal([]byte(next.Value), &newProfile) != nil || newProfile.Version <= oldProfile.Version {
		return errors.New("studio.ai_profile must be valid JSON with an increased version")
	}
	if expected != nil {
		var oldFields, newFields map[string]any
		if json.Unmarshal([]byte(previous.Value), &oldFields) != nil ||
			json.Unmarshal([]byte(next.Value), &newFields) != nil || oldFields == nil || newFields == nil {
			return errors.New("invalid studio.ai_profile")
		}
		for _, field := range []string{"version", "models", "defaults"} {
			delete(oldFields, field)
			delete(newFields, field)
		}
		if !reflect.DeepEqual(oldFields, newFields) {
			return errors.New("the model tier editor preserves the existing provider, route and other profile settings")
		}
	}
	return validateProfileModels(newProfile, expected != nil)
}

func validateProfileModels(profile profileModels, tiers bool) error {
	if len(profile.Models) != 2 || len(profile.Defaults) != 2 {
		return errors.New("text and vision models and defaults are required")
	}
	for _, kind := range []string{"text", "vision"} {
		models := profile.Models[kind]
		if len(models) == 0 || len(models) > 200 || (tiers && len(models) != 3) ||
			!slices.Contains(models, profile.Defaults[kind]) {
			return errors.New("select a default from the configured models for text and vision")
		}
		seen := make(map[string]bool, len(models))
		for _, id := range models {
			if id == "" || len([]rune(id)) > 200 || strings.TrimSpace(id) != id || seen[id] {
				return errors.New("model IDs must be nonempty and distinct within each group")
			}
			seen[id] = true
		}
	}
	return nil
}
