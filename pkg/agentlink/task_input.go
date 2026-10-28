package agentlink

import (
	"context"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type TaskGuidance struct {
	From      protocol.AgentRef `json:"from"`
	ID        string            `json:"id"`
	RequestID string            `json:"request_id"`
	Text      string            `json:"text"`
}
type TaskInputBackend interface {
	Input(context.Context, TaskGuidance) (Task, error)
}

func validGuidance(r TaskGuidance, node string) bool {
	return taskCaller(r.From) && r.From.NodeID == node && core.Identifier(r.ID) && core.Identifier(r.RequestID) && r.Text != "" && len(r.Text) <= 24000
}

// Both in-process and peer dispatch use the receiver's operation contract. Task
// ownership remains enforced by Status/Input on the original authenticated caller.
