package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/storage"
	"strings"
)

func readCommand(ctx context.Context, q querier, control, task, run, id string) (*Command, error) {
	var c Command
	var reply sql.NullString
	err := q.QueryRowContext(ctx, "SELECT command_id,digest,phase,reply FROM run_commands WHERE control_id=? AND task_id=? AND run_id=? AND command_id=?", control, task, run, id).Scan(&c.ID, &c.Digest, &c.Phase, &reply)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if reply.Valid {
		c.Reply = json.RawMessage(reply.String)
	}
	return &c, nil
}
func (j *Journal) InspectCommand(ctx context.Context, control, task, run, id string) (*Command, error) {
	return readCommand(ctx, j.db, control, task, run, id)
}

// BeginCommand commits the command identity before effects. Only a newly-created
// receipt returns claim=true. A lost/pending receipt is inspected, never replayed.
func (j *Journal) BeginCommand(ctx context.Context, control, task, run, id string, body json.RawMessage) (command Command, claim bool, err error) {
	if id == "" || len(id) > 512 || strings.ContainsRune(id, 0) || len(body) > 4*1024*1024 {
		return command, false, fmt.Errorf("invalid command identity/payload")
	}
	hash, err := core.HashJSON(body)
	if err != nil {
		return command, false, err
	}
	err = storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		old, err := readCommand(ctx, tx, control, task, run, id)
		if err != nil {
			return err
		}
		if old != nil {
			if old.Digest != hash {
				return fmt.Errorf("command receipt conflicts with original payload")
			}
			command = *old
			return nil
		}
		record, err := readRun(ctx, tx, control, task, run)
		if err != nil {
			return err
		}
		if record == nil || record.Phase != Running || record.BootID != j.BootID || record.Finalizing || record.StopRequested != "" {
			return fmt.Errorf("run is not current")
		}
		var unknown int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM run_commands WHERE control_id=? AND task_id=? AND run_id=? AND phase='unknown'", control, task, run).Scan(&unknown); err != nil {
			return err
		}
		if unknown > 0 {
			return core.ErrCleanupUnconfirmed
		}
		command = Command{ID: id, Digest: hash, Phase: "pending"}
		_, err = tx.ExecContext(ctx, "INSERT INTO run_commands(control_id,task_id,run_id,command_id,digest,phase) VALUES(?,?,?,?,?,'pending')", control, task, run, id, hash)
		claim = err == nil
		return err
	})
	return command, claim, err
}

// Settle closes command admission atomically with checking every issued receipt.
// This runs inside the shared Execution invocation before its slot can release.
func (j *Journal) Settle(ctx context.Context, control, task, run string) error {
	return j.changeTx(ctx, control, task, run, func(tx *sql.Tx, record *Run) error {
		if record.Phase != Running || record.BootID != j.BootID {
			return core.ErrCleanupUnconfirmed
		}
		var pending int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM run_commands WHERE control_id=? AND task_id=? AND run_id=? AND phase!='complete'", control, task, run).Scan(&pending); err != nil {
			return err
		}
		if pending > 0 {
			return core.ErrCleanupUnconfirmed
		}
		record.Finalizing = true
		return nil
	})
}
func (j *Journal) CompleteCommand(ctx context.Context, control, task, run, id string, reply json.RawMessage) error {
	if !json.Valid(reply) || len(reply) > 16*1024*1024 {
		return fmt.Errorf("invalid command reply")
	}
	return storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		old, err := readCommand(ctx, tx, control, task, run, id)
		if err != nil {
			return err
		}
		if old == nil {
			return fmt.Errorf("command was not accepted")
		}
		if old.Phase == "complete" {
			a, err := core.HashJSON(old.Reply)
			if err != nil {
				return err
			}
			b, err := core.HashJSON(reply)
			if err != nil {
				return err
			}
			if a != b {
				return fmt.Errorf("conflicting command completion")
			}
			return nil
		}
		_, err = tx.ExecContext(ctx, "UPDATE run_commands SET phase='complete',reply=? WHERE control_id=? AND task_id=? AND run_id=? AND command_id=?", string(reply), control, task, run, id)
		return err
	})
}
func (j *Journal) MarkCommandUnknown(ctx context.Context, control, task, run, id string) error {
	return storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		command, err := readCommand(ctx, tx, control, task, run, id)
		if err != nil {
			return err
		}
		if command == nil {
			return fmt.Errorf("command was not accepted")
		}
		if command.Phase == "complete" {
			return nil
		}
		record, err := readRun(ctx, tx, control, task, run)
		if err != nil {
			return err
		}
		if record == nil || record.Phase.Ended() {
			return fmt.Errorf("run cannot become uncertain")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE run_commands SET phase='unknown' WHERE control_id=? AND task_id=? AND run_id=? AND command_id=?", control, task, run, id); err != nil {
			return err
		}
		record.Phase = RecoveryRequired
		record.CleanupConfirmed = false
		return writeRun(ctx, tx, *record)
	})
}
