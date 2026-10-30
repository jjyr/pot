package control

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/core"
)

// ExperimentUsage is a local operator projection; it never queries remote ledgers.
type ExperimentUsage struct {
	usage.Breakdown
	UsageAvailable bool                          `json:"usage_available"`
	Turn           string                        `json:"turn,omitempty"`
	Communications *assistant.CommunicationUsage `json:"communications"`
	ToolRecords    *usage.Page                   `json:"tool_records,omitempty"`
}

func (s *Service) ExperimentUsage(ctx context.Context, owner, task string, q usage.Query, details bool, at int64) (ExperimentUsage, error) {
	result := ExperimentUsage{UsageAvailable: true, Turn: q.Turn}
	if owner != "" && task != "" || owner != "" && !core.Identifier(owner) || task != "" && !core.Identifier(task) || q.Turn != "" && (!core.Identifier(q.Turn) || owner == "" && task == "") {
		return result, errors.New("invalid usage scope")
	}
	if owner != "" {
		defs, err := s.Registry.Definitions(ctx)
		if err != nil {
			return result, err
		}
		found := false
		for _, d := range defs {
			if d.ID == owner {
				found = true
			}
		}
		if !found {
			return result, errors.New("local assistant not found")
		}
		q.Scope = owner
	} else if task != "" {
		var taskOwner string
		if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT assistant_id FROM tasks WHERE id=?", task).Scan(&taskOwner); err != nil {
			return result, err
		}
		record, err := s.Tasks.Get(ctx, taskOwner, task)
		if err != nil {
			return result, err
		}
		if record == nil {
			return result, errors.New("task not found")
		}
		result.UsageAvailable = record.Execution == nil
		q.Scope = task
	} else {
		q.Scope = ""
	}
	var err error
	result.Breakdown, err = s.Usage.Breakdown(ctx, q, at)
	if err != nil {
		return result, err
	}
	if details {
		page, e := s.Usage.ToolRecords(ctx, q, at)
		if e != nil {
			return result, e
		}
		result.ToolRecords = &page
	}
	if q.Turn == "" {
		start, stop, _, _, e := usage.Bounds(q, at)
		if e != nil {
			return result, e
		}
		// An entirely future interval has no observed messages yet.
		if stop < start {
			stop = start
		}
		messages, e := s.Mailbox.CommunicationUsage(ctx, q.Scope, start, stop, q.Offset)
		if e != nil {
			return result, e
		}
		if !details {
			messages.Items = nil
			messages.NextOffset = nil
		}
		result.Communications = &messages
	}
	return result, nil
}
