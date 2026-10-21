package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"time"
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
func (s *Service) syncLinkChats(ctx context.Context) {
	if !s.chatRecoveryMu.TryLock() {
		return
	}
	defer s.chatRecoveryMu.Unlock()
	rows, err := s.integrationDB.QueryContext(ctx, `SELECT id,link_id,assistant_id,sender,sender_kind FROM agent_link_chat WHERE role='user' AND reply_state IN ('pending','queued','running','waiting','paused','cancelling') ORDER BY checked_at,created_at LIMIT 4`)
	if err != nil {
		return
	}
	type pending struct{ id, link, assistant, sender, kind string }
	items := []pending{}
	for rows.Next() {
		var p pending
		if rows.Scan(&p.id, &p.link, &p.assistant, &p.sender, &p.kind) != nil {
			rows.Close()
			return
		}
		items = append(items, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	for _, p := range items {
		if ctx.Err() != nil {
			return
		}
		_, _ = s.integrationDB.ExecContext(ctx, "UPDATE agent_link_chat SET checked_at=? WHERE id=?", time.Now().UnixMilli(), p.id)
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		result, readErr := s.agentLinks.service.ChatStatus(callCtx, p.link, agentlink.ChatLookup{From: protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: p.kind, AgentID: p.sender}, AssistantID: p.assistant, MessageID: p.id})
		cancel()
		// An undelivered input has no receiver execution record yet.
		if errors.Is(readErr, agentlink.ErrNotFound) {
			continue
		}
		if readErr == nil {
			for _, m := range result.Messages {
				if _, readErr = (controlLinkMailbox{s}).Deliver(ctx, m); readErr != nil {
					break
				}
			}
		}
		if readErr != nil {
			changed, e := s.integrationDB.ExecContext(ctx, "UPDATE agent_link_chat SET reply_uncertain=1 WHERE id=? AND reply_uncertain=0", p.id)
			if e != nil {
				continue
			}
			if n, _ := changed.RowsAffected(); n == 0 {
				continue
			}
		} else {
			changed, e := s.integrationDB.ExecContext(ctx, "UPDATE agent_link_chat SET reply_state=?,reply_error=?,reply_uncertain=0 WHERE id=? AND (reply_state!=? OR reply_error!=? OR reply_uncertain!=0)", result.State, result.Error, p.id, result.State, result.Error)
			if e != nil {
				continue
			}
			if n, _ := changed.RowsAffected(); n == 0 {
				continue
			}
		}
		_, _ = s.integrationDB.ExecContext(ctx, "UPDATE agent_link_chat_sessions SET revision=revision+1 WHERE link_id=? AND assistant_id=?", p.link, p.assistant)
		s.Changes.Notify("agent-links")
	}
}
