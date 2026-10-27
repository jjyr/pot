package agentlink

import (
	"errors"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

var ErrCapacity = errors.New("agent_link_capacity_exceeded")
var ErrFenced = errors.New("agent_link_execution_fenced")
var ErrUnsupported = errors.New("agent_link_capability_unsupported")
var ErrDisconnected = errors.New("agent_link_disconnected")

func validateDirectory(d Directory, node string) error {
	if d.NodeID != node || len(d.Targets) > 257 || d.Name == "" || !core.Identifier(d.Adapter) {
		return ErrConflict
	}
	seen := map[protocol.AgentRef]bool{}
	for _, entry := range d.Targets {
		ref, description := entry.Address, entry.Capabilities
		scope := "peer"
		if ref.Kind == "node" {
			scope = "caller"
		}
		if !ValidTarget(ref) || !validIdentity(entry.Identity) || ref.NodeID != node || entry.Adapter != d.Adapter || seen[ref] || description.Version != 1 || description.Scope != scope || len(description.Operations) > 64 {
			return ErrConflict
		}
		seen[ref] = true
		operations := map[Operation]bool{}
		for _, op := range description.Operations {
			if op == "" || operations[op] {
				return ErrConflict
			}
			operations[op] = true
		}
	}
	if d.DefaultTarget != nil && !seen[*d.DefaultTarget] {
		return ErrConflict
	}

	return nil
}

// Directory refreshes the remote node's advertised assistants. It grants no
// access to its owner APIs, files, credentials or execution transport.

// Each request captures an epoch before network I/O. A confirmed deletion or
// newer request fences its response, including the value returned to callers.

func taskCaller(from protocol.AgentRef) bool {
	return from.Valid() && (from.Kind == "assistant" || from.Kind == "task" || from.Kind == "user")
}

// Disconnect pauses communication without deleting keys, history or work.

// Delete leaves a revocation tombstone so a stale peer cannot restore the link.
// The saved directory is removed; business history and running work are retained.

func validateSubmission(task Task, request TaskRequest, node, adapter string) error {
	if err := validateTask(task, node, adapter, "", request.RequestID); err != nil {
		return err
	}
	if request.Target.Kind == "node" && task.Receiver.Address.Kind != "task" || request.Target.Kind == "assistant" && task.Receiver.Address != request.Target {
		return ErrConflict
	}
	return nil
}

func validateTask(t Task, node, adapter, id, request string) error {
	receiver := t.Receiver
	if t.ID == "" || len(t.ID) > 256 || !validIdentity(receiver) || receiver.Adapter != adapter || (receiver.Address.Kind != "assistant" && (receiver.Address.Kind != "task" || receiver.Address.AgentID != t.ID)) || receiver.Address.NodeID != node || id != "" && t.ID != id || request != "" && t.RequestID != request || len(t.Result) > 1024*1024 || len(t.Error) > 65536 {
		return ErrConflict
	}
	switch t.State {
	case "queued", "waiting", "running", "waiting_approval", "cancelling", "complete", "completed", "failed", "cancelled", "interrupted", "unknown":
		return nil
	}
	return ErrConflict
}
