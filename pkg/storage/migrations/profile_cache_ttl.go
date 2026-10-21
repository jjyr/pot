package migrations

import (
	"encoding/json"
	"errors"
)

// UpgradeProfileCacheTTL replaces idle-delay intent with estimated cache lifetime.
// Explicit durations become TTL estimates; inherited defaults remain inherited.
func UpgradeProfileCacheTTL(raw []byte) ([]byte, bool, error) {
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, false, err
	}
	var version int
	if err := json.Unmarshal(file["version"], &version); err != nil {
		return nil, false, err
	}
	if version == 3 {
		return raw, false, nil
	}
	if version == 1 {
		upgraded, _, err := UpgradeProfileCompaction(raw)
		if err != nil {
			return nil, false, err
		}
		return UpgradeProfileCacheTTL(upgraded)
	}
	if version != 2 {
		return nil, false, errors.New("unsupported profile format")
	}
	upgrade := func(profile map[string]json.RawMessage) error {
		settings := map[string]json.RawMessage{}
		if body := profile["model_settings"]; len(body) > 0 && string(body) != "null" {
			if err := json.Unmarshal(body, &settings); err != nil {
				return err
			}
		}
		value := settings["idle_compact_minutes"]
		if len(value) == 0 {
			value = json.RawMessage("null")
		}
		settings["prompt_cache_minutes"] = value
		delete(settings, "idle_compact_minutes")
		delete(settings, "idle_compact_models")
		body, err := json.Marshal(settings)
		profile["model_settings"] = body
		return err
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(file["snapshot"], &snapshot); err != nil {
		return nil, false, err
	}
	var profiles []map[string]json.RawMessage
	if err := json.Unmarshal(snapshot["profiles"], &profiles); err != nil {
		return nil, false, err
	}
	for _, profile := range profiles {
		if err := upgrade(profile); err != nil {
			return nil, false, err
		}
	}
	snapshot["profiles"], _ = json.Marshal(profiles)
	file["snapshot"], _ = json.Marshal(snapshot)
	var receipts map[string]map[string]json.RawMessage
	if err := json.Unmarshal(file["requests"], &receipts); err != nil {
		return nil, false, err
	}
	for _, receipt := range receipts {
		var profile map[string]json.RawMessage
		if err := json.Unmarshal(receipt["profile"], &profile); err != nil {
			return nil, false, err
		}
		if err := upgrade(profile); err != nil {
			return nil, false, err
		}
		receipt["profile"], _ = json.Marshal(profile)
	}
	file["requests"], _ = json.Marshal(receipts)
	file["version"] = json.RawMessage("3")
	body, err := json.Marshal(file)
	return body, true, err
}
