package migrations

import (
	"encoding/json"
	"errors"
)

// UpgradeProfileCompaction adopts connection idle timing intent. Route,
// credentials, immutable execution snapshots and request receipts are retained.
func UpgradeProfileCompaction(raw []byte) ([]byte, bool, error) {
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, false, err
	}
	var version int
	if err := json.Unmarshal(file["version"], &version); err != nil {
		return nil, false, err
	}
	if version == 2 {
		return raw, false, nil
	}
	if version != 1 {
		return nil, false, errors.New("unsupported profile format")
	}
	// Materialize inherited timing intent in live profiles and immutable save
	// receipts. Do not change input hashes, route data or credential references.
	upgrade := func(profile map[string]json.RawMessage) error {
		settings := map[string]json.RawMessage{}
		if raw := profile["model_settings"]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &settings); err != nil {
				return err
			}
		}
		settings["idle_compact_minutes"] = json.RawMessage("null")
		body, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		profile["model_settings"] = body
		return nil
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
	file["version"] = json.RawMessage("2")
	body, err := json.Marshal(file)
	return body, true, err
}
