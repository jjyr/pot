package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/internal/agent"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/storage"
)

// Session pins one assistant's selected configuration for one correspondence;
// user-chat context and assistant configuration are independent of this store.
func (m *Mailbox) Session(ctx context.Context, message protocol.Envelope, initial core.ResolvedSpec) (core.ResolvedSpec, json.RawMessage, error) {
	if message.To.ControlID != m.controlID {
		return initial, nil, errors.New("mail session belongs to another Control")
	}
	if err := initial.Validate(); err != nil {
		return initial, nil, err
	}
	raw, err := json.Marshal(initial)
	if err != nil {
		return initial, nil, err
	}
	_, err = m.db.ExecContext(ctx, "INSERT INTO assistant_mail_sessions(assistant,conversation,config)VALUES(?,?,?)ON CONFLICT(assistant,conversation)DO NOTHING", message.To.AssistantID, message.ConversationID, string(raw))
	if err != nil {
		return initial, nil, err
	}
	var config []byte
	var state sql.NullString
	err = m.db.QueryRowContext(ctx, "SELECT config,context FROM assistant_mail_sessions WHERE assistant=? AND conversation=?", message.To.AssistantID, message.ConversationID).Scan(&config, &state)
	if err != nil {
		return initial, nil, err
	}
	var spec core.ResolvedSpec
	if err = core.DecodeJSON(config, &spec); err != nil {
		return spec, nil, err
	}
	var checkpoint json.RawMessage
	if state.Valid {
		checkpoint = json.RawMessage(state.String)
	}
	return spec, checkpoint, nil
}
func (m *Mailbox) Processing(ctx context.Context, message protocol.Envelope) error {
	var state string
	var raw []byte
	if err := m.db.QueryRowContext(ctx, "SELECT state,envelope FROM assistant_mail WHERE id=? AND direction='in'", message.ID).Scan(&state, &raw); err != nil {
		return err
	}
	var stored protocol.Envelope
	if err := core.DecodeJSON(raw, &stored); err != nil {
		return err
	}
	if state != "processing" || !sameEnvelope(stored, message) {
		return errors.New("mail processing attempt is fenced")
	}
	return nil
}
func (m *Mailbox) SaveSessionContext(ctx context.Context, message protocol.Envelope, state json.RawMessage) error {
	if !json.Valid(state) {
		return errors.New("mail checkpoint invalid")
	}
	return storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, "SELECT state FROM assistant_mail WHERE id=?", message.ID).Scan(&current); err != nil {
			return err
		}
		if current != "processing" {
			return errors.New("mail attempt is fenced")
		}
		_, err := tx.ExecContext(ctx, "UPDATE assistant_mail_sessions SET context=? WHERE assistant=? AND conversation=?", string(state), message.To.AssistantID, message.ConversationID)
		return err
	})
}
func (m *Mailbox) ClaimModel(ctx context.Context, message protocol.Envelope, id string) error {
	return storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		var state string
		var checkpoint []byte
		if err := tx.QueryRowContext(ctx, "SELECT m.state,s.context FROM assistant_mail m JOIN assistant_mail_sessions s ON s.assistant=m.recipient AND s.conversation=m.conversation WHERE m.id=?", message.ID).Scan(&state, &checkpoint); err != nil {
			return err
		}
		var parsed agent.Checkpoint
		if err := core.DecodeJSON(checkpoint, &parsed); err != nil {
			return err
		}
		if state != "processing" || parsed.ModelRequestID == nil || *parsed.ModelRequestID != id {
			return errors.New("mail model not durably checkpointed")
		}
		var count uint64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM assistant_mail_models WHERE message=?", message.ID).Scan(&count); err != nil {
			return err
		}
		if count >= 128 {
			return errors.New("mail turn model request limit reached")
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO assistant_mail_models(message,request)VALUES(?,?)", message.ID, id)
		return err
	})
}
func (m *Mailbox) AppendEvent(ctx context.Context, message protocol.Envelope, event core.AgentEvent) error {
	if err := m.Processing(ctx, message); err != nil {
		return err
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, "INSERT INTO assistant_mail_events(message,event)VALUES(?,?)", message.ID, string(raw))
	return err
}
