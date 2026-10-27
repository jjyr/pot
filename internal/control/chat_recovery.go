package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/protocol"
)

// Older projections omitted reply_to even though the immutable mailbox kept it.
// Recover only exact stored bindings; never infer a parent by display order.
func (s *Service) restoreChatReplyBindings(ctx context.Context) error {
	rows, err := s.integrationDB.QueryContext(ctx, "SELECT id FROM agent_link_chat WHERE role='assistant' AND reply_to=''")
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		var raw string
		if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT envelope FROM agent_messages WHERE id=?", id).Scan(&raw); err != nil {
			continue
		}
		var envelope protocol.Envelope
		if json.Unmarshal([]byte(raw), &envelope) != nil || envelope.ReplyTo == nil {
			continue
		}
		if err := s.captureLinkChat(ctx, envelope); err != nil {
			return err
		}
	}
	return nil
}

// syncLinkChats updates durable projections from receiver-owned execution facts.
// Reads never resubmit input, infer completion from prose, or replay model work.
func (s *Service) syncLinkChats(ctx context.Context) {}
