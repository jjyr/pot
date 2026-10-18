package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// Membership is a delivery journal over the inbox, never a second queue.
type MailBatch struct {
	ID       string
	Messages []protocol.Envelope
}

// CorrespondenceKey isolates peers by their full authenticated address, even
// when one peer starts multiple wire conversations or names match across Controls.
func CorrespondenceKey(message protocol.Envelope) string {
	key, _ := core.HashJSON([]protocol.AgentRef{message.To, message.From})
	return key
}

const sameMailGroup = `b.direction='in' AND json_extract(b.envelope,'$.kind')='message' AND b.recipient=m.recipient
 AND json_extract(b.envelope,'$.from.control_id')=json_extract(m.envelope,'$.from.control_id')
 AND json_extract(b.envelope,'$.from.kind')=json_extract(m.envelope,'$.from.kind')
 AND json_extract(b.envelope,'$.from.agent_id')=json_extract(m.envelope,'$.from.agent_id')`

const blockedMailGroup = ` AND NOT EXISTS(SELECT 1 FROM agent_messages b WHERE ` + sameMailGroup + ` AND
 (b.state IN ('processing','interrupted','failed') OR b.state='waiting' AND
 COALESCE((SELECT batch FROM agent_delivery_items WHERE message=b.id),b.id)!=COALESCE((SELECT batch FROM agent_delivery_items WHERE message=m.id),m.id)))`

func (m *Mailbox) ClaimMailBatch(ctx context.Context, seed protocol.Envelope) (MailBatch, error) {
	var batch MailBatch
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		var state string
		var body []byte
		err := tx.QueryRowContext(ctx, `SELECT state,envelope FROM agent_messages m WHERE id=? AND direction='in' AND state IN ('queued','waiting') AND json_extract(envelope,'$.kind')='message'`+blockedMailGroup, seed.ID).Scan(&state, &body)
		if err != nil {
			return err
		}
		var stored protocol.Envelope
		if err = json.Unmarshal(body, &stored); err != nil {
			return err
		}
		if !sameEnvelope(stored, seed) {
			return errors.New("mail batch seed changed")
		}
		if state == "waiting" {
			err = tx.QueryRowContext(ctx, "SELECT batch FROM agent_delivery_items WHERE message=?", seed.ID).Scan(&batch.ID)
			if err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, "SELECT envelope FROM agent_messages WHERE id IN (SELECT message FROM agent_delivery_items WHERE batch=?) AND state='waiting' ORDER BY sequence", batch.ID)
			if err != nil {
				return err
			}
			batch.Messages, err = readBatchRows(rows)
			if err != nil {
				return err
			}
			if len(batch.Messages) == 0 || batch.Messages[0].ID != batch.ID {
				return errors.New("waiting batch membership is incomplete")
			}
		} else {
			rows, err := tx.QueryContext(ctx, `SELECT envelope FROM agent_messages WHERE direction='in' AND state='queued' AND recipient=? AND json_extract(envelope,'$.kind')='message' AND json_extract(envelope,'$.from.control_id')=? AND json_extract(envelope,'$.from.kind')=? AND json_extract(envelope,'$.from.agent_id')=? ORDER BY sequence LIMIT ?`, seed.To.AgentID, seed.From.ControlID, seed.From.Kind, seed.From.AgentID, inbox.MaxBatchMessages)
			if err != nil {
				return err
			}
			batch.Messages, err = readBatchRows(rows)
			if err != nil {
				return err
			}
			if len(batch.Messages) == 0 || batch.Messages[0].ID != seed.ID {
				return errors.New("mail batch seed is not oldest")
			}
			batch.ID = seed.ID
		}
		for _, message := range batch.Messages {
			if err := claimBatchItem(ctx, tx, batch.ID, message.ID, state); err != nil {
				return err
			}
		}
		return nil
	})
	return batch, err
}

func readBatchRows(rows *sql.Rows) ([]protocol.Envelope, error) {
	defer rows.Close()
	var messages []protocol.Envelope
	size := 0
	for rows.Next() {
		var body []byte
		var message protocol.Envelope
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &message); err != nil {
			return nil, err
		}
		if len(messages) > 0 && size+len(message.Text) > inbox.MaxBatchBytes {
			break
		}
		messages = append(messages, message)
		size += len(message.Text)
	}
	return messages, rows.Err()
}

func claimBatchItem(ctx context.Context, tx *sql.Tx, batch, id, state string) error {
	result, err := tx.ExecContext(ctx, "UPDATE agent_messages SET state='processing' WHERE id=? AND state=?", id, state)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("batch member already claimed")
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO agent_delivery_items(batch,message) VALUES(?,?) ON CONFLICT(batch,message) DO NOTHING", batch, id)
	return err
}

func (m *Mailbox) ClaimEventBatch(ctx context.Context, owner string, ids []string) error {
	if len(ids) == 0 || len(ids) > inbox.MaxBatchMessages {
		return errors.New("invalid result batch size")
	}
	return storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		size := 0
		for _, id := range ids {
			var body string
			if err := tx.QueryRowContext(ctx, `SELECT envelope FROM agent_messages WHERE id=? AND recipient=? AND state='queued' AND json_extract(envelope,'$.kind') IN ('task_result','plugin_event')`, id, owner).Scan(&body); err != nil {
				return err
			}
			size += len(body)
			if size > inbox.MaxBatchBytes && len(ids) > 1 {
				return errors.New("result batch content limit exceeded")
			}
			if err := claimBatchItem(ctx, tx, ids[0], id, "queued"); err != nil {
				return err
			}
		}
		return nil
	})
}
