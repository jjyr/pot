package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// ResumeDelivery restores only the original journal membership after the owning
// conversation admits a verified continuation. Ordinary inbox polling and sender
// tools cannot reopen a failed or interrupted receiver attempt.
func (m *Mailbox) ResumeDelivery(ctx context.Context, owner, batch string) error {
	return storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		var count, invalid int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN recipient!=? OR direction!='in' OR state NOT IN ('processing','failed','interrupted') THEN 1 ELSE 0 END),0)
 FROM agent_messages WHERE id IN (SELECT message FROM agent_delivery_items WHERE batch=?)`, owner, batch).Scan(&count, &invalid); err != nil {
			return err
		}
		if count == 0 || invalid != 0 {
			return errors.New("original delivery cannot resume")
		}
		_, err := tx.ExecContext(ctx, `UPDATE agent_messages SET state='processing',detail=NULL
 WHERE id IN (SELECT message FROM agent_delivery_items WHERE batch=?)`, batch)
		return err
	})
}

// Notifications reads both trusted notification sources in inbox receipt order.
// Correspondence uses its sender batch but shares the same owning conversation.
func (m *Mailbox) Notifications(ctx context.Context, owner string) ([]protocol.Envelope, error) {
	events, err := m.envelopes(ctx, `SELECT envelope FROM agent_messages WHERE recipient=? AND direction='in' AND state='queued' AND json_extract(envelope,'$.to.node_id')=? AND json_extract(envelope,'$.to.kind')='assistant' AND json_extract(envelope,'$.kind') IN ('task_result','plugin_event') ORDER BY sequence LIMIT ?`, owner, m.nodeID, inbox.MaxBatchMessages)
	if err != nil {
		return nil, err
	}
	size := 0
	for i, event := range events {
		body, _ := json.Marshal(event)
		size += len(body)
		if size > inbox.MaxBatchBytes {
			// Keep the existing result policy: an individually large Task outcome
			// travels alone, rather than permanently blocking this assistant's inbox.
			if i == 0 {
				return events[:1], nil
			}
			return events[:i], nil
		}
	}
	return events, nil
}

func (m *Mailbox) ObserveTaskResult(ctx context.Context, owner, turn, task string) error {
	_, err := m.db.ExecContext(ctx, `INSERT INTO agent_result_observations(owner,turn,message)
 SELECT ?,?,id FROM agent_messages WHERE recipient=? AND json_extract(envelope,'$.kind')='task_result'
 AND json_extract(envelope,'$.result.task_id')=? AND state='queued' ON CONFLICT DO NOTHING`, owner, turn, owner, task)
	return err
}

func (m *Mailbox) ClaimEvent(ctx context.Context, owner, id string) error {
	return m.ClaimEventBatch(ctx, owner, []string{id})
}
func (m *Mailbox) ReleaseEvent(ctx context.Context, id string) error {
	return storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE agent_messages SET state='queued' WHERE (id=? OR id IN (SELECT message FROM agent_delivery_items WHERE batch=?)) AND state='processing' AND json_extract(envelope,'$.kind') IN ('task_result','plugin_event','message')", id, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM agent_delivery_items WHERE batch=?", id)
		return err
	})
}

func (m *Mailbox) FinishChat(ctx context.Context, owner, turn, event string, success bool) error {
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		if success {
			if _, err := tx.ExecContext(ctx, `UPDATE agent_messages SET state='processed' WHERE state='queued' AND id IN (SELECT message FROM agent_result_observations WHERE owner=? AND turn=?)`, owner, turn); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM agent_result_observations WHERE owner=? AND turn=?", owner, turn); err != nil {
			return err
		}
		if event != "" {
			state := "failed"
			if success {
				state = "processed"
			}
			_, err := tx.ExecContext(ctx, "UPDATE agent_messages SET state=?,detail=NULL WHERE (id=? OR id IN (SELECT message FROM agent_delivery_items WHERE batch=?)) AND recipient=? AND state IN ('processing','interrupted') AND json_extract(envelope,'$.kind') IN ('task_result','plugin_event','message')", state, event, event, owner)
			return err
		}
		return nil
	})
	if err == nil {
		m.Changes.Notify("assistant-mail")
	}
	return err
}
