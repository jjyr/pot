package control

import (
	"context"
	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"os"
)

func (b potLinkBackend) Observe(ctx context.Context, peerNode string) (agentlink.Observation, error) {
	d, err := b.Directory(ctx)
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
	for _, assistant := range d.Assistants {
		id := assistant.Address.AgentID
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
	// Peer correspondence and notification handling can run outside the main
	// conversation. Observe their real manager admission rather than reporting idle.
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
	rows, err := b.s.Tasks.DB().QueryContext(ctx, "SELECT assistant_id,phase,COUNT(*) FROM tasks WHERE phase IN ('queued','running','waiting','waiting_approval') GROUP BY assistant_id,phase")
	if err != nil {
		return value, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, phase string
		var count int64
		if err = rows.Scan(&id, &phase, &count); err != nil {
			return value, err
		}
		if a := byID[id]; a != nil {
			switch phase {
			case "queued":
				a.Queued = count
			case "running":
				a.Running = count
			case "waiting":
				a.WaitingAllowance = count
			case "waiting_approval":
				a.WaitingApproval = count
			}
		}
	}
	return value, rows.Err()
}
