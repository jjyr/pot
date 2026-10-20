package bridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func consumerTx(ctx context.Context, tx *sql.Tx, consumer string) error {
	if !validID(consumer) {
		return errors.New("codex_consumer_required")
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO codex_mail_consumers(id) VALUES(?)", consumer); err != nil {
		return err
	}
	var active bool
	if err := tx.QueryRowContext(ctx, "SELECT active FROM codex_mail_consumers WHERE id=?", consumer).Scan(&active); err != nil {
		return err
	}
	if !active {
		return errors.New("codex_consumer_recovered_start_new_chat")
	}
	return nil
}
func (m *hostMailbox) claim(ctx context.Context, consumer string) ([]protocol.MailRecord, error) {
	items := []protocol.MailRecord{}
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		if err := consumerTx(ctx, tx, consumer); err != nil {
			return err
		}
		read := func(query string, args ...any) error {
			rows, err := tx.QueryContext(ctx, query, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			size := 0
			for rows.Next() {
				var item protocol.MailRecord
				var raw string
				if err = rows.Scan(&item.Sequence, &raw, &item.State, &item.Detail); err != nil {
					return err
				}
				if size+len(raw) > inbox.MaxBatchBytes {
					break
				}
				size += len(raw)
				if err = json.Unmarshal([]byte(raw), &item.Message); err != nil {
					return err
				}
				items = append(items, item)
			}
			return rows.Err()
		}
		if err := read("SELECT m.sequence,m.envelope,m.state,m.detail FROM agent_messages m JOIN agent_delivery_items d ON d.message=m.id WHERE d.batch=? AND m.direction='incoming' AND m.state='read' ORDER BY m.sequence LIMIT 8", consumer); err != nil {
			return err
		}
		if len(items) > 0 {
			return nil
		}
		if err := read("SELECT m.sequence,m.envelope,m.state,m.detail FROM agent_messages m WHERE m.direction='incoming' AND m.state='queued' AND NOT EXISTS(SELECT 1 FROM agent_delivery_items d WHERE d.message=m.id) ORDER BY m.sequence LIMIT 8"); err != nil {
			return err
		}
		var parent protocol.Envelope
		var previous sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT parent_id FROM codex_mail_consumers WHERE id=?", consumer).Scan(&previous); err != nil {
			return err
		}
		if previous.Valid {
			var raw string
			if err := tx.QueryRowContext(ctx, "SELECT envelope FROM agent_messages WHERE id=?", previous.String).Scan(&raw); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(raw), &parent); err != nil {
				return err
			}
		}
		for i := range items {
			item := &items[i]
			if _, err := tx.ExecContext(ctx, "INSERT INTO agent_delivery_items(batch,message) VALUES(?,?)", consumer, item.Message.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE agent_messages SET state='read' WHERE id=?", item.Message.ID); err != nil {
				return err
			}
			item.State = "read"
			if parent.ID == "" || item.Message.Hops >= parent.Hops {
				parent = item.Message
			}
		}
		if parent.ID != "" {
			_, err := tx.ExecContext(ctx, "UPDATE codex_mail_consumers SET parent_id=? WHERE id=?", parent.ID, consumer)
			return err
		}
		return nil
	})
	return items, err
}
func (m *hostMailbox) ackConsumer(ctx context.Context, consumer, id string) error {
	return storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		if err := consumerTx(ctx, tx, consumer); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "UPDATE agent_messages SET state='processed' WHERE id=? AND direction='incoming' AND EXISTS(SELECT 1 FROM agent_delivery_items WHERE batch=? AND message=agent_messages.id)", id, consumer)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return errors.New("codex_message_not_claimed")
		}
		return nil
	})
}
func (m *hostMailbox) recoverConsumers(ctx context.Context) error {
	return storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE codex_mail_consumers SET active=0 WHERE EXISTS(SELECT 1 FROM agent_delivery_items d JOIN agent_messages m ON m.id=d.message WHERE d.batch=codex_mail_consumers.id AND m.state='read')"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE agent_messages SET state='queued' WHERE direction='incoming' AND (state='interrupted' OR state='read' AND EXISTS(SELECT 1 FROM agent_delivery_items d JOIN codex_mail_consumers c ON c.id=d.batch WHERE d.message=agent_messages.id AND c.active=0))"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM agent_delivery_items WHERE message IN(SELECT id FROM agent_messages WHERE direction='incoming' AND state='queued') AND batch IN(SELECT id FROM codex_mail_consumers WHERE active=0)")
		return err
	})
}
