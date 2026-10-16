package runner

import (
	"context"
	"github.com/jjyr/pot/pkg/core"
)

// SandboxTaskReferences includes ended original runs because their fixed
// execution workspace remains the authority for retained Task file delivery.
func (j *Journal) SandboxTaskReferences(ctx context.Context, id string) ([]string, error) {
	rows, err := j.db.QueryContext(ctx, "SELECT body FROM task_runs")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var run Run
		if err = core.DecodeJSON(raw, &run); err != nil {
			return nil, err
		}
		target := run.Launch.ExecutionTarget
		if target.RuntimeID == j.RuntimeID && target.Target.Kind == "sandbox" && target.Target.SandboxID == id && !seen[run.Launch.TaskID] {
			refs = append(refs, "task:"+run.Launch.TaskID)
			seen[run.Launch.TaskID] = true
		}
	}
	return refs, rows.Err()
}
