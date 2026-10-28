package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// Automatic retries have a durable one-to-one receipt per source Task. A failed
// replacement may receive its own recovery after its result is delivered.
func (s *Service) CanRetry(ctx context.Context, assistant, task string) (bool, error) {
	r, err := s.Get(ctx, assistant, task)
	if err != nil || r == nil {
		return false, err
	}
	if !retryEligible(r) {
		return false, nil
	}
	var used bool
	err = s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM task_retries WHERE assistant_id=? AND source_task=?)", assistant, task).Scan(&used)
	return !used, err
}

func retryEligible(r *Record) bool {
	return r != nil && r.Execution == nil && r.Info.Phase == protocol.TaskFailed && r.Origin.Kind == "manual" && r.StopMode == "" && r.Info.Pending == 0 && !r.Info.Archived
}

// Retry preserves the original execution selection and instructions. Only the
// owning notification receiver may authorize this operation; the model supplies
// a repair approach, not a replacement goal, destination or model.
func (s *Service) Retry(ctx context.Context, assistant, source, instructions string) (Record, error) {
	if strings.TrimSpace(instructions) == "" || len(instructions) > 8192 {
		return Record{}, fmt.Errorf("retry requires a concise repair approach")
	}
	var record Record
	err := storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		// Receipt, eligibility and input snapshot share the replacement transaction.
		// A concurrent repeat must return before enqueuing any replay inputs.
		var existing string
		err := tx.QueryRowContext(ctx, "SELECT task_id FROM task_retries WHERE assistant_id=? AND source_task=?", assistant, source).Scan(&existing)
		if err == nil {
			r, err := s.GetTx(ctx, tx, assistant, existing)
			if err != nil || r == nil {
				return fmt.Errorf("retry receipt unavailable: %v", err)
			}
			record = *r
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		original, err := s.GetTx(ctx, tx, assistant, source)
		if err != nil {
			return err
		}
		if !retryEligible(original) {
			return fmt.Errorf("original task no longer permits recovery")
		}
		request := original.Request
		request.RequestID = "retry-" + source
		selection := Selection{Config: *original.Config, Plugins: original.Plugins}
		record, err = s.EnqueueTx(ctx, tx, assistant, request, selection, protocol.TaskOrigin{Kind: "manual"}, storage.Now())
		if err != nil {
			return err
		}
		if err := replayRetryInputs(ctx, tx, assistant, source, &record); err != nil {
			return err
		}
		text := "\n\nRecovery of the failed attempt:\n" + instructions + "\nPreserve the original goal and constraints. Inspect existing effects before repeating operations; never repeat a denied operation. The previous workspace is not imported."
		sender := protocol.AgentRef{NodeID: s.options.NodeID, Kind: "assistant", AgentID: assistant}
		hash, err := inputDigest(currentInputDigestFormat, text, "guidance", sender, nil)
		if err != nil {
			return err
		}
		id := ID()
		repair := protocol.Envelope{ID: id, Kind: "guidance", Owner: assistant, RequestID: id, Digest: hash, From: sender, To: *record.Info.Address, ConversationID: record.RunID, Text: text}
		if err := inbox.EnqueueTx(ctx, tx, repair, "in", "pending"); err != nil {
			return err
		}
		record.Info.Pending++
		if err := write(ctx, tx, &record); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO task_retries(assistant_id,source_task,task_id) VALUES(?,?,?)", assistant, source, record.Info.ID)
		return err
	})
	if err == nil {
		s.Notify()
	}
	return record, err
}

// Re-address accepted inputs without changing their authority or causal evidence.
// They are pending in the new run and consumed by the ordinary Agent boundary;
// the original inbox and its consumption states remain untouched.
func replayRetryInputs(ctx context.Context, tx *sql.Tx, assistant, source string, replacement *Record) error {
	rows, err := tx.QueryContext(ctx, `SELECT envelope FROM agent_messages
 WHERE recipient=? AND json_extract(envelope,'$.owner')=?
 AND json_extract(envelope,'$.to.kind')='task' AND json_extract(envelope,'$.kind')!='input' ORDER BY sequence`, source, assistant)
	if err != nil {
		return err
	}
	var messages []protocol.Envelope
	for rows.Next() {
		var raw string
		var message protocol.Envelope
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			rows.Close()
			return err
		}
		messages = append(messages, message)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, message := range messages {
		originalID := message.ID
		message.ID, err = core.HashJSON([]string{replacement.Info.ID, originalID})
		if err != nil {
			return err
		}
		message.To, message.ConversationID = *replacement.Info.Address, replacement.RunID
		if err := inbox.EnqueueTx(ctx, tx, message, "in", "pending"); err != nil {
			return err
		}
		// Historical digest receipts still bind the original sender and parent.
		if _, err := tx.ExecContext(ctx, "INSERT INTO task_input_digest_formats(message,format) SELECT ?,format FROM task_input_digest_formats WHERE message=?", message.ID, originalID); err != nil {
			return err
		}
		if message.Kind == "guidance" || message.Kind == "user_guidance" {
			replacement.Info.Pending++
		}
	}
	return nil
}
