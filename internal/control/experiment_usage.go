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
	Accounting        usage.Accounting              `json:"accounting"`
	ActivityAvailable bool                          `json:"activity_available"`
	UsageAvailable    bool                          `json:"usage_available"`
	Turn              string                        `json:"turn,omitempty"`
	Communications    *assistant.CommunicationUsage `json:"communications"`
	ToolRecords       *usage.Page                   `json:"tool_records,omitempty"`
}

func (s *Service) ExperimentUsage(ctx context.Context, owner, task string, q usage.Query, details bool, at int64) (ExperimentUsage, error) {
	result := ExperimentUsage{UsageAvailable: true, Turn: q.Turn}
	if owner != "" && task != "" || owner != "" && !core.Identifier(owner) || task != "" && !core.Identifier(task) || q.Turn != "" && (!core.Identifier(q.Turn) || owner == "" && task == "") {
		return result, errors.New("invalid usage scope")
	}
	if owner != "" {
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
	result.Accounting, err = s.Usage.Accounting(ctx, usage.AccountingQuery{Details: details, Query: q, Assistant: owner, Task: task, Request: q.Request, Model: q.Model, Provider: q.Provider, GroupBy: q.GroupBy}, at)
	if err != nil {
		return result, err
	}
	if q.Request != "" || q.Model != "" || q.Provider != "" {
		_, _, first, last, e := usage.Bounds(q, at)
		if e != nil {
			return result, e
		}
		zone := q.Timezone
		if zone == "" {
			zone = "Asia/Shanghai"
		}
		result.Breakdown = usage.Breakdown{Start: first, End: last, Timezone: zone, Scope: q.Scope}
		return result, nil
	}
	result.ActivityAvailable = true
	result.Breakdown, err = s.Usage.Breakdown(ctx, q, at)
	if err != nil {
		return result, err
	}
	if details && q.Request == "" && q.Model == "" && q.Provider == "" {
		page, e := s.Usage.ToolRecords(ctx, q, at)
		if e != nil {
			return result, e
		}
		result.ToolRecords = &page
	}
	if q.Turn == "" && q.Request == "" && q.Model == "" && q.Provider == "" {
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
