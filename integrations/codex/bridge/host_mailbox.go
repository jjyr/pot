package bridge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// hostMailbox uses the product's existing inbox schema. Transport receipts and
// Codex's explicit read/processed acknowledgments are deliberately separate.
type hostMailbox struct {
	db    *sql.DB
	local protocol.AgentRef
}

func (m *hostMailbox) Pending(ctx context.Context, local, peer protocol.AgentRef) ([]protocol.Envelope, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT envelope FROM agent_messages WHERE direction='outgoing' AND state='queued' AND json_extract(envelope,'$.from.control_id')=? AND json_extract(envelope,'$.from.agent_id')=? AND json_extract(envelope,'$.to.control_id')=? AND json_extract(envelope,'$.to.agent_id')=? ORDER BY sequence LIMIT 32`, local.ControlID, local.AgentID, peer.ControlID, peer.AgentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []protocol.Envelope
	for rows.Next() {
		var raw string
		var e protocol.Envelope
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, err
		}
		if e.From == local && e.To == peer {
			result = append(result, e)
		}
	}
	return result, rows.Err()
}
func (m *hostMailbox) Deliver(ctx context.Context, e protocol.Envelope) (protocol.Receipt, error) {
	if e.Kind != "message" || e.To != m.local {
		return protocol.Receipt{}, errors.New("agent_link_target_mismatch")
	}
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error { return inbox.EnqueueTx(ctx, tx, e, "incoming", "queued") })
	return protocol.Receipt{Message: e}, err
}
func (m *hostMailbox) Acknowledge(ctx context.Context, r protocol.Receipt) error {
	raw, _ := json.Marshal(r.Message)
	res, err := m.db.ExecContext(ctx, "UPDATE agent_messages SET state='received' WHERE id=? AND envelope=? AND direction='outgoing'", r.Message.ID, string(raw))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("agent_link_receipt_mismatch")
	}
	return nil
}
func (m *hostMailbox) send(ctx context.Context, peer protocol.AgentRef, requestID, text string) (protocol.Envelope, error) {
	return m.sendConsumer(ctx, "", peer, requestID, text)
}
func (m *hostMailbox) sendConsumer(ctx context.Context, consumer string, peer protocol.AgentRef, requestID, text string) (protocol.Envelope, error) {
	if !validID(requestID) {
		return protocol.Envelope{}, errors.New("invalid_request_id")
	}
	digest := sha256.Sum256([]byte(m.local.ControlID + "\x00" + requestID))
	id := hex.EncodeToString(digest[:])
	uuid := digest
	uuid[6] = (uuid[6] & 15) | 64
	uuid[8] = (uuid[8] & 63) | 128
	conv := fmt.Sprintf("%x-%x-%x-%x-%x", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
	e := protocol.Envelope{ID: id, Kind: "message", From: m.local, To: peer, ConversationID: conv, Text: text}
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		if consumer != "" {
			if err := consumerTx(ctx, tx, consumer); err != nil {
				return err
			}
		}
		var previous string
		err := tx.QueryRowContext(ctx, "SELECT envelope FROM agent_messages WHERE id=? AND direction='outgoing'", id).Scan(&previous)
		if err == nil {
			var old protocol.Envelope
			if err = json.Unmarshal([]byte(previous), &old); err != nil {
				return err
			}
			if old.From != m.local || old.To != peer || old.Text != text {
				return errors.New("message identity has different content")
			}
			e = old
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if consumer != "" {
			var parentID sql.NullString
			if err = tx.QueryRowContext(ctx, "SELECT parent_id FROM codex_mail_consumers WHERE id=?", consumer).Scan(&parentID); err != nil {
				return err
			}
			if parentID.Valid {
				var raw string
				var parent protocol.Envelope
				if err = tx.QueryRowContext(ctx, "SELECT envelope FROM agent_messages WHERE id=?", parentID.String).Scan(&raw); err != nil {
					return err
				}
				if err = json.Unmarshal([]byte(raw), &parent); err != nil {
					return err
				}
				if parent.Hops >= protocol.MaxMailHops {
					return errors.New("agent_message_hop_limit")
				}
				e.Hops = parent.Hops + 1
				if parent.From == peer {
					e.ReplyTo = &parent.ID
					e.ConversationID = parent.ConversationID
				}
			}
		}
		return inbox.EnqueueTx(ctx, tx, e, "outgoing", "queued")
	})
	return e, err
}
func (m *hostMailbox) messages(ctx context.Context) ([]protocol.MailRecord, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT sequence,envelope,state,detail FROM agent_messages WHERE direction='incoming' AND state IN ('queued','read') ORDER BY sequence LIMIT 8")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []protocol.MailRecord{}
	bytes := 0
	for rows.Next() {
		var r protocol.MailRecord
		var raw string
		if err = rows.Scan(&r.Sequence, &raw, &r.State, &r.Detail); err != nil {
			return nil, err
		}
		if bytes+len(raw) > inbox.MaxBatchBytes {
			break
		}
		bytes += len(raw)
		if err = json.Unmarshal([]byte(raw), &r.Message); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}
