package control

import (
	"encoding/json"
	"github.com/jjyr/pot/pkg/executor"
)

// Original command receipt formats are retained only for recovery evidence.
type remoteExecute struct {
	Operation Operation        `json:"operation"`
	Request   executor.Request `json:"request"`
}
type executeReply struct {
	Value              json.RawMessage `json:"value"`
	Error              string          `json:"error"`
	CleanupUnconfirmed bool            `json:"cleanup_unconfirmed"`
}
