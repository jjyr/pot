package control

import (
	"context"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/internal/tasks"
)

// SandboxReferences protects retained Task artifacts as well as active work.
// Schedule placement is machine-owned; a local plan protects the currently
// selected Sandbox until the owner changes or deletes that plan.
func (s *Service) SandboxReferences(ctx context.Context, id string, selected bool) ([]string, error) {
	refs, err := s.RunnerJournal.SandboxTaskReferences(ctx, id)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		seen[ref] = true
	}
	add := func(ref string) {
		if !seen[ref] {
			refs = append(refs, ref)
			seen[ref] = true
		}
	}
	rows, err := s.Tasks.DB().QueryContext(ctx, "SELECT body FROM tasks")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var task tasks.Record
		if err = core.DecodeJSON(raw, &task); err != nil {
			rows.Close()
			return nil, err
		}
		if task.Info.ExecutionTarget != nil {
			target := task.Info.ExecutionTarget
			if target.RuntimeID == s.options.RuntimeID && target.Target.Kind == "sandbox" && target.Target.SandboxID == id {
				add("task:" + task.Info.ID)
			}
		} else if selected && task.Request.MachineID == s.options.RuntimeID && task.Info.Phase.Scope() == "active" {
			add("task:" + task.Info.ID)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if selected {
		definitions, err := s.Registry.Definitions(ctx)
		if err != nil {
			return nil, err
		}
		for _, assistant := range definitions {
			for offset := uint64(0); ; {
				plans, err := s.Schedules.Page(ctx, assistant.ID, schedule.Query{Scope: "all", Limit: 50, Offset: offset})
				if err != nil {
					return nil, err
				}
				for _, plan := range plans.Items {
					if plan.Deleted {
						continue
					}
					machine, err := s.Tasks.ResolveMachine(ctx, plan.Spec.MachineID)
					if err != nil {
						return nil, err
					}
					if machine == s.options.RuntimeID {
						add("schedule:" + plan.ID)
					}
				}
				if plans.NextOffset == nil {
					break
				}
				offset = *plans.NextOffset
			}
		}
	}
	return refs, nil
}
