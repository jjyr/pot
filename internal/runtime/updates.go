package runtime

import (
	"context"

	"github.com/jjyr/pot/internal/selfupdate"
	"github.com/jjyr/pot/pkg/execution"
)

func (r *Runtime) updateWork() (selfupdate.Work, error) {
	work := selfupdate.Work{}
	local, err := r.Control.Tasks.LocalActive(context.Background())
	if err != nil {
		return work, err
	}
	work.Tasks = len(local)
	definitions, err := r.Control.Registry.Definitions(context.Background())
	if err != nil {
		return work, err
	}
	for _, d := range definitions {
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
	local, err := r.Control.Tasks.LocalActive(ctx)
	if err != nil {
		return err
	}
	for _, task := range local {
		if err := r.Control.Tasks.Stop(ctx, task.AssistantID, task.ID, "interrupt"); err != nil {
			return err
		}
	}
	return nil
}
