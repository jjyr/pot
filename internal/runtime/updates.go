package runtime

import (
	"context"

	"github.com/jjyr/pot/internal/selfupdate"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

func (r *Runtime) updateWork() (selfupdate.Work, error) {
	work := selfupdate.Work{}
	definitions, err := r.Control.Registry.Definitions(context.Background())
	if err != nil {
		return work, err
	}
	for _, d := range definitions {
		page, err := r.Control.Tasks.Page(context.Background(), d.ID, protocol.TaskQuery{Scope: "active", Limit: 1})
		if err != nil {
			return work, err
		}
		work.Tasks += int(page.Counts.Active)
		for _, session := range r.Control.Terminals.List(d.ID) {
			if session.ExitCode == nil {
				work.Sessions++
			}
		}
	}
	for _, run := range r.Executions.Snapshot() {
		if run.Phase == execution.Running || run.Phase == execution.Queued || run.Phase == execution.Interrupted {
			if run.Role != "task" {
				work.Replies++
			}
		}
	}
	return work, nil
}

// Persist explicit interruption for queued as well as accepted Tasks. Shutdown
// alone can leave never-launched work eligible for dispatch on the next boot.
func (r *Runtime) interruptUpdateTasks(ctx context.Context) error {
	definitions, err := r.Control.Registry.Definitions(ctx)
	if err != nil {
		return err
	}
	for _, d := range definitions {
		var items []protocol.TaskInfo
		for offset := uint64(0); ; {
			page, err := r.Control.Tasks.Page(ctx, d.ID, protocol.TaskQuery{Scope: "active", Limit: 50, Offset: offset})
			if err != nil {
				return err
			}
			items = append(items, page.Items...)
			if page.NextOffset == nil {
				break
			}
			offset = *page.NextOffset
		}
		for _, task := range items {
			if err := r.Control.Tasks.Stop(ctx, d.ID, task.ID, "interrupt"); err != nil {
				return err
			}
		}
	}
	return nil
}
