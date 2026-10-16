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
	return m.envelopes(ctx, `SELECT envelope FROM assistant_mail m WHERE direction='in' AND state IN ('queued','waiting') AND recipient=? AND NOT EXISTS(SELECT 1 FROM assistant_mail b WHERE b.conversation=m.conversation AND b.recipient=m.recipient AND (b.state IN ('processing','interrupted','failed') OR b.state='waiting' AND b.id!=m.id)) ORDER BY sequence LIMIT 32`, recipient)
}
func (m *Mailbox) WaitingMessages(ctx context.Context, recipient string) ([]protocol.Envelope, error) {
	return m.envelopes(ctx, "SELECT envelope FROM assistant_mail WHERE direction='in' AND state='waiting' AND recipient=? ORDER BY sequence LIMIT 32", recipient)
}

// Wait retains only a fully stopped pre-request checkpoint; unknown model/tool
// attempts remain interrupted and can never enter the resumable queue.
func (m *Mailbox) Wait(ctx context.Context, message protocol.Envelope, detail string) error {
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		var state string
		var raw []byte
		if err := tx.QueryRowContext(ctx, "SELECT m.state,s.context FROM assistant_mail m JOIN assistant_mail_sessions s ON s.assistant=m.recipient AND s.conversation=m.conversation WHERE m.id=?", message.ID).Scan(&state, &raw); err != nil {
			return err
		}
		var checkpoint agent.Checkpoint
		if err := core.DecodeJSON(raw, &checkpoint); err != nil {
			return err
		}
		if state != "processing" || checkpoint.LastRequest != message.ID || checkpoint.Started != nil || checkpoint.ModelRequestID != nil || len(checkpoint.Pending) > 0 {
			return errors.New("mail attempt is not safely allowance-resumable")
		}
		_, err := tx.ExecContext(ctx, "UPDATE assistant_mail SET state='waiting',detail=? WHERE id=?", detail, message.ID)
		return err
	})
	if err == nil {
		m.Changes.Notify("assistant-mail")
	}
	return err
}
