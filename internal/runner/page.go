package runner

import (
	"context"
	"github.com/jjyr/pot/internal/core"
)

func (j *Journal) Runs(ctx context.Context) ([]Run, error) {
	rows, err := j.db.QueryContext(ctx, "SELECT body FROM task_runs ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var run Run
		if err = core.DecodeJSON(raw, &run); err != nil {
			return nil, err
		}
		if err = j.validateRun(run); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}
