package assistant

import (
	"context"
	"errors"
	"strings"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// DeliveryMail reads immutable correspondence from the original batch journal.
// It remains available for history projection after the turn is acknowledged.
func (m *Mailbox) DeliveryMail(ctx context.Context, owner, turn string) ([]protocol.Envelope, error) {
	if !strings.HasPrefix(turn, "pot-result-") {
		return nil, nil
	}
	return m.envelopes(ctx, `SELECT envelope FROM agent_messages
 WHERE id IN (SELECT message FROM agent_delivery_items WHERE batch=?)
 AND recipient=? AND direction='in' AND json_extract(envelope,'$.kind')='message'
 AND json_extract(envelope,'$.to.kind')='assistant'
 AND json_extract(envelope,'$.from.kind') IN ('assistant','task','user') ORDER BY sequence`, strings.TrimPrefix(turn, "pot-result-"), owner)
}

func (m *Mailbox) Processing(ctx context.Context, message protocol.Envelope) error {
	var state string
	var raw []byte
	if err := m.db.QueryRowContext(ctx, "SELECT state,envelope FROM agent_messages WHERE id=? AND direction='in'", message.ID).Scan(&state, &raw); err != nil {
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
