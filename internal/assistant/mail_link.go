package assistant

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
)

// PendingNode filters the immutable node route before the page bound. Accepted
// messages retain their sender even if its assistant is later removed.
func (m *Mailbox) PendingNode(ctx context.Context, local, peer string) ([]protocol.Envelope, error) {
	return m.envelopes(ctx, `SELECT envelope FROM agent_messages WHERE direction='out' AND state='queued' AND id NOT IN (SELECT id FROM retired_agent_link_work WHERE kind='message') AND json_extract(envelope,'$.kind')='message' AND json_extract(envelope,'$.from.node_id')=? AND json_extract(envelope,'$.to.node_id')=? ORDER BY sequence LIMIT ?`, local, peer, protocol.MailPageSize)
}
