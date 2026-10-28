package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// Run reads a previous immutable local run receipt or the current run.
func (s *Service) Run(ctx context.Context, owner, task, run string) (*Record, error) {
	current, err := s.Get(ctx, owner, task)
	if err != nil || current == nil || current.RunID == run {
		return current, err
	}
	var raw []byte
	err = s.db.QueryRowContext(ctx, "SELECT body FROM task_run_history WHERE assistant_id=? AND task_id=? AND run_id=?", owner, task, run).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record Record
	if err = core.DecodeJSON(raw, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// continuePending starts a new execution identity only after the old run's
// terminal receipt and hook have committed. Remaining guidance stays queued.
func (s *Service) continuePending(ctx context.Context, owner, task string) error {
	return s.change(ctx, owner, task, func(tx *sql.Tx, r *Record) error {
		if r.Info.Phase.Scope() == "active" {
			return nil
		}
		var id, text string
		err := tx.QueryRowContext(ctx, "SELECT request_id,text FROM task_inputs WHERE assistant_id=? AND task_id=? AND state='pending' AND kind IN ('guidance','user_guidance') ORDER BY CASE WHEN kind='user_guidance' THEN 0 ELSE 1 END,sequence LIMIT 1", owner, task).Scan(&id, &text)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		return s.beginContinuationTx(ctx, tx, r, id, text)
	})
}

// SetDispositions applies an owner decision batch atomically. Run identity and
// pending input fence old decisions; workspace contents are deliberately irrelevant.
func (s *Service) SetDispositions(ctx context.Context, owner string, decisions []protocol.TaskDisposition) ([]protocol.TaskInfo, error) {
	if len(decisions) == 0 || len(decisions) > 8 {
		return nil, fmt.Errorf("expected 1–8 task decisions")
	}
	result := []protocol.TaskInfo{}
	err := storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		seen := map[string]bool{}
		for _, d := range decisions {
			if seen[d.TaskID] || !core.Identifier(d.TaskID) || !core.Identifier(d.RunID) || len(d.Reason) > 2048 {
				return fmt.Errorf("invalid task decision")
			}
			seen[d.TaskID] = true
			r, err := read(ctx, tx, owner, d.TaskID)
			if err != nil {
				return err
			}
			if r == nil || r.RunID != d.RunID || r.Info.Phase.Scope() == "active" || r.Info.Pending > 0 {
				return protocol.APIError{Code: "task_disposition_conflict", Message: "task decision is stale or task still has work"}
			}
			r.Info.Archived, r.Info.ArchiveReason = d.Archived, strings.TrimSpace(d.Reason)
			r.Info.UpdatedAtMS = storage.Now()
			if err := write(ctx, tx, r); err != nil {
				return err
			}
			result = append(result, r.Info)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.Notify()
	return result, nil
}

// beginContinuationTx is shared by local guidance and authenticated receiver control.
func (s *Service) beginContinuationTx(ctx context.Context, tx *sql.Tx, r *Record, id, text string) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO task_run_history(assistant_id,task_id,run_id,body)VALUES(?,?,?,?)", r.AssistantID, r.Info.ID, r.RunID, string(raw)); err != nil {
		return err
	}
	r.PreviousRunID = r.RunID
	r.RunID = ID()
	r.Info.RunID = r.RunID
	r.RunText, r.RunInputID = text, id
	r.Info.Phase = protocol.TaskQueued
	r.Info.Archived, r.Info.Paused, r.Info.RemoteWaiting = false, false, false
	r.Info.ArchiveReason = ""
	r.Info.StartedAtMS = 0
	r.Info.FinishedAtMS = 0
	r.StopMode, r.Error = "", ""
	r.Finalizing = false
	r.Launch, r.Result, r.Outcome = nil, nil, nil
	r.Origin = protocol.TaskOrigin{Kind: "manual"}
	if r.Info.Pending > 0 {
		r.Info.Pending--
	}
	_, err = tx.ExecContext(ctx, "UPDATE task_inputs SET state='consumed' WHERE assistant_id=? AND task_id=? AND request_id=?", r.AssistantID, r.Info.ID, id)
	return err
}
