package assistant

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func (m *Mailbox) Candidates(ctx context.Context, recipient string) ([]protocol.Envelope, error) {
	return m.envelopes(ctx, `SELECT envelope FROM agent_messages m WHERE direction='in' AND json_extract(envelope,'$.kind')='message' AND json_extract(envelope,'$.to.kind')='assistant' AND state IN ('queued','waiting') AND recipient=?`+blockedMailGroup+` ORDER BY sequence LIMIT 32`, recipient)
}
func (m *Mailbox) WaitingMessages(ctx context.Context, recipient string) ([]protocol.Envelope, error) {
	return m.envelopes(ctx, "SELECT envelope FROM agent_messages WHERE direction='in' AND state='waiting' AND recipient=? ORDER BY sequence LIMIT 32", recipient)
}

// Wait retains only a fully stopped pre-request checkpoint; unknown model/tool
// attempts remain interrupted and can never enter the resumable queue.
func (m *Mailbox) Wait(ctx context.Context, message protocol.Envelope, detail string) error {
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		var state string
		var raw []byte
		if err := tx.QueryRowContext(ctx, "SELECT m.state,s.context FROM agent_messages m JOIN assistant_mail_sessions s ON s.assistant=m.recipient AND s.conversation=? WHERE m.id=?", CorrespondenceKey(message), message.ID).Scan(&state, &raw); err != nil {
			return err
		}
		var checkpoint agent.Checkpoint
		if err := core.DecodeJSON(raw, &checkpoint); err != nil {
			return err
		}
		if state != "processing" || checkpoint.LastRequest != message.ID || checkpoint.Started != nil || checkpoint.ModelRequestID != nil || len(checkpoint.Pending) > 0 {
			return errors.New("mail attempt is not safely allowance-resumable")
		}
		_, err := tx.ExecContext(ctx, "UPDATE agent_messages SET state='waiting',detail=? WHERE state='processing' AND (id=? OR id IN (SELECT message FROM agent_delivery_items WHERE batch=?))", detail, message.ID, message.ID)
		return err
	})
	if err == nil {
		m.Changes.Notify("assistant-mail")
	}
	return err
}
