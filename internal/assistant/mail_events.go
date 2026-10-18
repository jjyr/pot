package assistant

import (
	"context"
	"database/sql"

	"github.com/jjyr/pot/pkg/storage"
)

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
		if _, err := tx.ExecContext(ctx, "UPDATE agent_messages SET state='queued' WHERE (id=? OR id IN (SELECT message FROM agent_delivery_items WHERE batch=?)) AND state='processing' AND json_extract(envelope,'$.kind')='task_result'", id, id); err != nil {
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
			_, err := tx.ExecContext(ctx, "UPDATE agent_messages SET state=? WHERE (id=? OR id IN (SELECT message FROM agent_delivery_items WHERE batch=?)) AND recipient=? AND state='processing' AND json_extract(envelope,'$.kind')='task_result'", state, event, event, owner)
			return err
		}
		return nil
	})
	if err == nil {
		m.Changes.Notify("assistant-mail")
	}
	return err
}
