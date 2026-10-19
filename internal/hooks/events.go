// Package hooks routes trusted Runtime lifecycle events to UI projections and,
// for Task outcomes, the existing durable Agent inbox. It owns no extra queue.
package hooks

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type Kind string

const (
	ApprovalRequested Kind = "approval_requested"
	ApprovalResolved  Kind = "approval_resolved"
	ApprovalExpired   Kind = "approval_expired"
	TaskStarted       Kind = "task_started"
	TaskCompleted     Kind = "task_completed"
	TaskFailed        Kind = "task_failed"
	TaskInterrupted   Kind = "task_interrupted"
	TaskCancelled     Kind = "task_cancelled"
)

// Event references authoritative state. Approval events carry no executable
// arguments or decision authority and never become Agent messages.
type Event struct {
	ID         string
	Kind       Kind
	Owner      string
	TaskID     string
	RunID      string
	Generation uint64
	ApprovalID string
	Decision   string
	Result     *protocol.TaskResultEvent
}

type Publish func(Event)

func (e Event) Validate() error {
	if !core.Identifier(e.ID) || !core.Identifier(e.Owner) || !core.Identifier(e.RunID) || e.Generation == 0 {
		return fmt.Errorf("invalid lifecycle hook identity")
	}
	switch e.Kind {
	case TaskStarted:
		if !core.Identifier(e.TaskID) || e.Result != nil || e.ApprovalID != "" {
			return fmt.Errorf("invalid Task start hook")
		}
	case ApprovalRequested, ApprovalResolved, ApprovalExpired:
		if !core.Identifier(e.ApprovalID) || e.Result != nil {
			return fmt.Errorf("invalid approval hook")
		}
	case TaskCompleted, TaskFailed, TaskInterrupted, TaskCancelled:
		if e.Result == nil || e.ApprovalID != "" || e.Result.TaskID != e.TaskID || e.Result.RunID != e.RunID || taskKind(e.Result.Phase) != e.Kind {
			return fmt.Errorf("invalid Task outcome hook")
		}
	default:
		return fmt.Errorf("unknown lifecycle hook")
	}
	return nil
}

// Topics wakes existing UI projections. Approval cards and user reminders read
// current pending decisions; a delayed invalidation cannot resurrect an approval.
func (e Event) Topics() []string {
	switch e.Kind {
	case TaskStarted:
		return []string{"tasks"}
	case ApprovalRequested, ApprovalResolved, ApprovalExpired:
		return []string{"permissions", "tasks"}
	case TaskCompleted, TaskFailed, TaskInterrupted, TaskCancelled:
		return []string{"tasks", "assistant-mail"}
	}
	return nil
}

func Approval(kind Kind, id, owner, task, run string, generation uint64, decision string) Event {
	eventID, _ := core.HashJSON([]string{id, string(kind), decision})
	return Event{ID: eventID, Kind: kind, Owner: owner, TaskID: task, RunID: run, Generation: generation, ApprovalID: id, Decision: decision}
}

func Task(owner string, generation uint64, result protocol.TaskResultEvent) Event {
	id, _ := core.HashJSON([]string{result.TaskID, result.RunID, "terminal"})
	return Event{ID: id, Kind: taskKind(result.Phase), Owner: owner, TaskID: result.TaskID, RunID: result.RunID, Generation: generation, Result: &result}
}

func Started(owner, task, run string, generation uint64) Event {
	id, _ := core.HashJSON([]string{task, run, "started"})
	return Event{ID: id, Kind: TaskStarted, Owner: owner, TaskID: task, RunID: run, Generation: generation}
}

func taskKind(phase protocol.TaskPhase) Kind {
	switch phase {
	case protocol.TaskComplete:
		return TaskCompleted
	case protocol.TaskFailed:
		return TaskFailed
	case protocol.TaskInterrupted:
		return TaskInterrupted
	case protocol.TaskCancelled:
		return TaskCancelled
	}
	return ""
}

// EnqueueTx atomically delivers the Task outcome alongside its terminal state.
// UI-only approval hooks cannot use this path to wake an assistant model.
func EnqueueTx(ctx context.Context, tx *sql.Tx, control, runtime string, event Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if event.Result == nil {
		return fmt.Errorf("UI-only hooks have no Agent inbox recipient")
	}
	message := protocol.Envelope{ID: event.ID, Kind: "task_result", From: protocol.AgentRef{NodeID: control, Kind: "runtime", AgentID: runtime}, To: protocol.AgentRef{NodeID: control, Kind: "assistant", AgentID: event.Owner}, ConversationID: event.RunID, Text: event.Result.Text, Result: event.Result}
	state := "queued"
	if event.Result.SuppressionReason != "" {
		state = "suppressed"
	}
	return inbox.EnqueueTx(ctx, tx, message, "in", state)
}
