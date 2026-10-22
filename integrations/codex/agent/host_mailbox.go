package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// hostMailbox uses the product's existing inbox schema. Transport receipts and
// Admission and automatic session processing acknowledgments are separate.
type hostMailbox struct {
	db      *sql.DB
	local   protocol.AgentRef
	backend *backend
}

func (m *hostMailbox) PendingNode(ctx context.Context, local, peer string) ([]protocol.Envelope, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT envelope FROM agent_messages WHERE direction='outgoing' AND state='queued' AND json_extract(envelope,'$.from.node_id')=? AND json_extract(envelope,'$.to.node_id')=? ORDER BY sequence LIMIT 32`, local, peer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.Envelope{}
	for rows.Next() {
		var raw string
		var value protocol.Envelope
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
func (m *hostMailbox) Deliver(ctx context.Context, e protocol.Envelope) (protocol.Receipt, error) {
	if e.From.Kind != "assistant" && e.From.Kind != "task" {
		return protocol.Receipt{}, agentlink.ErrUnsupported
	}
	if e.Kind != "message" || e.To.Kind != "assistant" || e.To.NodeID != m.local.NodeID {
		return protocol.Receipt{}, errors.New("agent_link_target_mismatch")
	}
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		if m.backend != nil {
			var accepted bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agent_messages WHERE id=? AND direction='incoming')", e.ID).Scan(&accepted); err != nil {
				return err
			}
			if !accepted {
				if err := activeAssistantTx(ctx, tx, e.To.AgentID); err != nil {
					return err
				}
			}
		}
		return inbox.EnqueueTx(ctx, tx, e, "incoming", "queued")
	})
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
func (m *hostMailbox) sendFrom(ctx context.Context, from, peer protocol.AgentRef, requestID, text, executionID string) (protocol.Envelope, error) {
	if !validID(requestID) {
		return protocol.Envelope{}, errors.New("invalid_request_id")
	}
	digest := sha256.Sum256([]byte(encoded(from) + "\x00" + requestID))
	id := hex.EncodeToString(digest[:])
	uuid := digest
	uuid[6] = (uuid[6] & 15) | 64
	uuid[8] = (uuid[8] & 63) | 128
	conv := fmt.Sprintf("%x-%x-%x-%x-%x", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
	e := protocol.Envelope{ID: id, Kind: "message", From: from, To: peer, ConversationID: conv, Text: text}
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		var previous string
		err := tx.QueryRowContext(ctx, "SELECT envelope FROM agent_messages WHERE id=? AND direction='outgoing'", id).Scan(&previous)
		if err == nil {
			var old protocol.Envelope
			if err = json.Unmarshal([]byte(previous), &old); err != nil {
				return err
			}
			if old.From != from || old.Text != text || old.To.NodeID != peer.NodeID || old.To != peer {
				return errors.New("message identity has different content")
			}
			e = old
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// Bind the exact executing message, never another concurrent job.
		if executionID != "" {
			var raw string
			if err := tx.QueryRowContext(ctx, `SELECT data FROM codex_link_jobs WHERE id=? AND assistant_id=? AND json_extract(data,'$.task.state') IN ('running','waiting_approval')`, executionID, from.AgentID).Scan(&raw); err != nil {
				return err
			}
			var current job
			if err := json.Unmarshal([]byte(raw), &current); err != nil {
				return err
			}
			if parent := current.Message; parent != nil {
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
