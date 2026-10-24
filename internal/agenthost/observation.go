package agenthost

import (
	"context"
	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/agentlink"
	"os"
)

func (b *backend) Observe(ctx context.Context, peerNode string) (agentlink.Observation, error) {
	list, err := b.list(ctx)
	value := agentlink.Observation{NodeID: b.nodeID, Assistants: []agentlink.Activity{}}
	if err != nil {
		return value, err
	}
	value.Hostname, _ = os.Hostname()
	value.Executor = &agentlink.Activity{ChatState: "idle"}
	value.Outbox.Pending, value.Outbox.OldestAtMS, err = inbox.OutboxStats(ctx, b.db, "outgoing", b.nodeID, peerNode)
	if err != nil {
		return value, err
	}
	for _, a := range list {
		if b.runtime().Kind == "antigravity" && a.Model == "" {
			continue
		}
		value.Assistants = append(value.Assistants, agentlink.Activity{AssistantID: a.ID, ChatState: "idle"})
	}
	byID := map[string]*agentlink.Activity{}
	for index := range value.Assistants {
		byID[value.Assistants[index].AssistantID] = &value.Assistants[index]
	}
	rows, err := b.db.QueryContext(ctx, `SELECT COALESCE(assistant_id,''),json_extract(data,'$.task.state'),COALESCE(json_type(data,'$.message')='object',0),COALESCE(json_extract(data,'$.cleanup_uncertain'),0),COUNT(*) FROM agent_adapter_jobs WHERE json_extract(data,'$.task.state') IN ('queued','running','cancelling','waiting_approval') OR json_extract(data,'$.cleanup_uncertain')=1 GROUP BY 1,2,3,4`)
	if err != nil {
		return value, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, state string
		var message, fenced bool
		var count int64
		if err = rows.Scan(&id, &state, &message, &fenced, &count); err != nil {
			return value, err
		}
		a := byID[id]
		if id == "" {
			a = value.Executor
		}
		if a == nil {
			continue
		}
		a.Fenced = a.Fenced || fenced
		if message {
			if state == "running" || state == "cancelling" || state == "waiting_approval" {
				a.ChatState = "running"
			} else if state == "queued" && a.ChatState == "idle" {
				a.ChatState = "queued"
			}
			continue
		}
		switch state {
		case "queued":
			a.Queued += count
		case "running", "cancelling":
			a.Running += count
		case "waiting_approval":
			a.WaitingApproval += count
		}
	}
	return value, rows.Err()
}
