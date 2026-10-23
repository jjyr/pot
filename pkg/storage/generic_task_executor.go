package storage

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"modernc.org/sqlite"
)

// The preceding creation contract is frozen for historical request hashes.
type legacyCreateTask struct {
	Executor        string                    `json:"executor,omitempty"`
	LinkID          string                    `json:"link_id,omitempty"`
	Target          *protocol.AgentRef        `json:"target,omitempty"`
	Workspace       protocol.TaskWorkspace    `json:"workspace"`
	MachineID       string                    `json:"machine_id"`
	Skills          *[]string                 `json:"skills"`
	RequestID       string                    `json:"request_id"`
	Text            string                    `json:"text"`
	Profile         *string                   `json:"profile"`
	Model           *string                   `json:"model"`
	Requirements    protocol.TaskRequirements `json:"requirements"`
	SelectionReason *string                   `json:"selection_reason,omitempty"`
}

// Frozen Agent Link TaskRequest field order for the generic-executor upgrade.
// Receiver payload receipts compare the exact encoding, including omitempty and
// HTML escaping. Storage cannot import agentlink, which itself uses storage.
type executorUpgradeRequest struct {
	Executor      string            `json:"executor,omitempty"`
	Configuration *legacyCreateTask `json:"configuration,omitempty"`
	RequestID     string            `json:"request_id"`
	From          protocol.AgentRef `json:"from"`
	AssistantID   string            `json:"assistant_id,omitempty"`
	Text          string            `json:"text"`
	Workspace     string            `json:"workspace,omitempty"`
}

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("pot_agent_task_payload", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		raw, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("task payload must be JSON text")
		}
		var request executorUpgradeRequest
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			return nil, err
		}
		if request.Executor != "codex" {
			return nil, fmt.Errorf("unexpected historical executor")
		}
		request.Executor = "agent"
		encoded, err := json.Marshal(request)
		return string(encoded), err
	})
	sqlite.MustRegisterDeterministicScalarFunction("pot_agent_task_digest", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		raw, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("task record must be JSON text")
		}
		var record struct {
			Request legacyCreateTask    `json:"request"`
			Origin  protocol.TaskOrigin `json:"origin"`
		}
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, err
		}
		if record.Request.Executor != "codex" {
			return nil, fmt.Errorf("unexpected historical executor")
		}
		record.Request.Executor = "agent"
		return core.HashJSON([]any{record.Request, record.Origin})
	})
}
