package control

import (
	"context"
	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"os"
)

func (b potLinkBackend) Observe(ctx context.Context, peerNode string) (agentlink.Observation, error) {
	definitions, err := b.s.Registry.Definitions(ctx)
	value := agentlink.Observation{NodeID: b.s.options.RuntimeID, Assistants: []agentlink.Activity{}}
	if err != nil {
		return value, err
	}
	value.Hostname, _ = os.Hostname()
	value.Outbox.Pending, value.Outbox.OldestAtMS, err = inbox.OutboxStats(ctx, b.s.Tasks.DB(), "out", value.NodeID, peerNode)
	if err != nil {
		return value, err
	}
	byID := map[string]*agentlink.Activity{}
	for _, assistant := range definitions {
		id := assistant.ID
		activity := agentlink.Activity{AssistantID: id, ChatState: "unknown"}
		b.s.mu.Lock()
		conversation := b.s.conversations[id]
		b.s.mu.Unlock()
		if conversation != nil {
			activity.ChatState, activity.Fenced = conversation.ExecutionState()
		}
		value.Assistants = append(value.Assistants, activity)
	}
	for index := range value.Assistants {
		byID[value.Assistants[index].AssistantID] = &value.Assistants[index]
	}
	// Include the owning conversation's manager admission and cleanup fences,
	// even before a conversation projection has been loaded.
	for _, active := range b.s.options.Manager.Snapshot() {
		if active.Phase == execution.Interrupted {
			owner := active.Owner
			if active.Role == "task" {
				if err := b.s.Tasks.DB().QueryRowContext(ctx, "SELECT assistant_id FROM tasks WHERE id=?", active.Owner).Scan(&owner); err != nil {
					continue
				}
			}
			if a := byID[owner]; a != nil {
				a.Fenced = true
			}
			continue
		}
		if active.Role == "task" {
			continue
		}
		if a := byID[active.Owner]; a != nil {
			if active.Phase == execution.Running {
				a.ChatState = "running"
			}
			if active.Phase == execution.Queued && (a.ChatState == "idle" || a.ChatState == "unknown") {
				a.ChatState = "queued"
			}
		}
	}
	activity, err := b.s.Tasks.LocalActivity(ctx)
	if err != nil {
		return value, err
	}
	for _, item := range activity {
		if a := byID[item.AssistantID]; a != nil {
			switch item.Phase {
			case "queued":
				a.Queued = item.Count
			case "running":
				a.Running = item.Count
			case "waiting":
				a.WaitingAllowance = item.Count
			case "waiting_approval":
				a.WaitingApproval = item.Count
			}
		}
	}
	return value, nil
}
