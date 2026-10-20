// Package migrations also contains pure upgrades for persisted non-SQL JSON.
package migrations

import (
	"encoding/json"
	"errors"
)

// UpgradeServerConfig removes retired remote owner-listener settings. This
// changes configuration only; it never starts listeners, connects or replays work.
func UpgradeServerConfig(raw []byte) ([]byte, bool, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false, err
	}
	if value == nil {
		return nil, false, errors.New("invalid server settings")
	}
	if v, ok := value["version"]; ok {
		var version int
		if json.Unmarshal(v, &version) != nil || version != 1 {
			return nil, false, errors.New("unsupported server settings version")
		}
		return raw, false, nil
	}
	delete(value, "listen")
	delete(value, "tls_cert")
	delete(value, "tls_key")
	value["version"] = json.RawMessage("1")
	body, err := json.Marshal(value)
	return body, true, err
}
