package runner

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
)

type CommandRecord struct {
	Command
	Input json.RawMessage `json:"input"`
}

func (j *Journal) Commands(ctx context.Context, control, task, run string) ([]CommandRecord, error) {
	rows, err := j.db.QueryContext(ctx, "SELECT c.command_id,c.digest,c.phase,c.reply,COALESCE(i.body,'null') FROM run_commands c LEFT JOIN run_command_inputs i USING(control_id,task_id,run_id,command_id) WHERE c.control_id=? AND c.task_id=? AND c.run_id=? ORDER BY c.command_id", control, task, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []CommandRecord{}
	size := 0
	for rows.Next() {
		var record CommandRecord
		var reply *string
		var input string
		if err = rows.Scan(&record.ID, &record.Digest, &record.Phase, &reply, &input); err != nil {
			return nil, err
		}
		record.Input = json.RawMessage(input)
		if reply != nil {
			record.Reply = json.RawMessage(*reply)
		}
		if input != "null" {
			hash, err := core.HashJSON(record.Input)
			if err != nil || hash != record.Digest {
				return nil, errors.New("original command input checksum mismatch")
			}
		}
		size += len(input) + len(record.Reply)
		if size > 12*1024*1024 || len(records) >= 10000 {
			return nil, errors.New("original command evidence exceeds bounded inspection limit")
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// RecordCleanup is written only after the original injected cleanup service has
// confirmed its effects stopped. Restart preserves this evidence, never handles.
func (j *Journal) RecordCleanup(ctx context.Context, control, task, run string) error {
	return j.change(ctx, control, task, run, func(r *Run) error {
		if r.BootID != j.BootID || (r.Phase != Running && r.Phase != RecoveryRequired) {
			return errors.New("cleanup evidence belongs to another original boot/run")
		}
		r.CleanupConfirmed = true
		return nil
	})
}

// RecoveryIdle rejects a reconciliation while the original invocation is still
// unwinding, even if it has already written its fenced journal state.
func (e *Executor) RecoveryIdle(control, task, run string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.active[key(control, task, run)] == nil
}
