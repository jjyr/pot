package assistant

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
)

// PendingLink filters the complete immutable route before applying the page
// bound. Other assistants at the same Control cannot starve or inspect it.
func (m *Mailbox) PendingLink(ctx context.Context, local, peer protocol.AgentRef) ([]protocol.Envelope, error) {
	return m.envelopes(ctx, `SELECT envelope FROM agent_messages WHERE direction='out' AND state='queued' AND json_extract(envelope,'$.kind')='message' AND json_extract(envelope,'$.from.control_id')=? AND json_extract(envelope,'$.from.kind')=? AND json_extract(envelope,'$.from.agent_id')=? AND json_extract(envelope,'$.to.control_id')=? AND json_extract(envelope,'$.to.kind')=? AND json_extract(envelope,'$.to.agent_id')=? ORDER BY sequence LIMIT ?`, local.ControlID, local.Kind, local.AgentID, peer.ControlID, peer.Kind, peer.AgentID, protocol.MailPageSize)
}
