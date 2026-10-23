package assistant

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
)

func (m *Mailbox) Candidates(ctx context.Context, recipient string) ([]protocol.Envelope, error) {
	return m.envelopes(ctx, `SELECT envelope FROM agent_messages m WHERE direction='in' AND json_extract(envelope,'$.kind')='message' AND json_extract(envelope,'$.to.kind')='assistant' AND state='queued' AND recipient=?`+blockedMailGroup+` ORDER BY sequence LIMIT 32`, recipient)
}
