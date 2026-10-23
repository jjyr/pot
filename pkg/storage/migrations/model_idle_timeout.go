package migrations

import (
	"encoding/json"
	"fmt"

	"github.com/jjyr/pot/pkg/core"
)

// UpgradeModelIdleSpec updates the live configuration, not historical execution
// snapshots or configuration receipts. Work identities and model history survive.
// Work on raw fields so this frozen migration never injects future defaults.
func UpgradeModelIdleSpec(raw string) (string, error) {
	var spec map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return "", err
	}
	var params, agent map[string]json.RawMessage
	if err := json.Unmarshal(spec["params"], &params); err != nil {
		return "", err
	}
	if err := json.Unmarshal(params["agent"], &agent); err != nil {
		return "", err
	}
	if spec == nil || params == nil || agent == nil {
		return "", fmt.Errorf("model timeout upgrade requires agent settings")
	}
	agent["request_timeout_ms"] = json.RawMessage("600000")
	params["agent"], _ = json.Marshal(agent)
	spec["params"], _ = json.Marshal(params)
	if _, resolved := spec["spec_hash"]; resolved {
		spec["spec_hash"] = json.RawMessage(`""`)
		hash, err := core.HashJSON(spec)
		if err != nil {
			return "", err
		}
		spec["spec_hash"], _ = json.Marshal(hash)
	}
	body, err := json.Marshal(spec)
	return string(body), err
}
