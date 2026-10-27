package control

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

// A direct browser chat is a user-kind caller. It is separate from the own-node
// assistant/task gateway, and never supplies a node execution binding.
func (s *Service) localPotMCPUICaller(ctx context.Context, c PotMCPConnection, assistant string) (*MCPAccess, agentlink.MCPCaller, error) {
	a := s.localPotMCPAccess()
	if a == nil || !c.Inbound || c.State != "active" {
		return nil, agentlink.MCPCaller{}, agentlink.ErrUnauthorized
	}
	caller, e := agentlink.NewMCPCaller(c.ID, protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "user", AgentID: linkChatID(c.ID, assistant)})
	return a, caller, e
}
func (s *Service) sendPotMCPChat(ctx context.Context, link, assistant, request, text string) (protocol.SendMessageAccepted, error) {
	var result protocol.SendMessageAccepted
	c, e := s.PotMCPConnection(ctx, link)
	if e != nil {
		return result, e
	}
	target := agentlink.AssistantTarget(c.RemoteNodeID, assistant)
	if c.Directory == nil || !c.Directory.Capabilities(target).Supports(agentlink.ChatSend) {
		return result, agentlink.ErrUnsupported
	}
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	var id, old string
	e = s.integrationDB.QueryRowContext(ctx, "SELECT id,text FROM agent_link_chat WHERE link_id=? AND assistant_id=? AND request_id=? AND role='user'", link, assistant, request).Scan(&id, &old)
	if e == nil {
		if old != text {
			return result, agentlink.ErrConflict
		}
		return protocol.SendMessageAccepted{MessageID: id, Duplicate: true}, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return result, e
	}
	input := MCPAccessMessageInput{RequestID: request, Message: protocol.SendMail{To: target, Text: text}}
	var accepted MCPAccessMessageAcceptance
	if c.Inbound {
		a, caller, err := s.localPotMCPUICaller(ctx, c, assistant)
		if err != nil {
			return result, err
		}
		accepted, e = a.message(ctx, caller, input)
	} else {
		e = s.potMCPCall(ctx, link, "pot_message_send", input, &accepted)
	}
	if e != nil {
		return result, e
	}
	message := accepted.Message.Message
	if message.From.Kind != "user" || message.To != target || message.Text != text {
		return result, agentlink.ErrUnauthorized
	}
	_, e = s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_chat_sessions(link_id,assistant_id,id) VALUES(?,?,?) ON CONFLICT DO NOTHING", link, assistant, linkChatID(link, assistant))
	if e != nil {
		return result, e
	}
	_, e = s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_chat(id,link_id,assistant_id,request_id,role,text,created_at,sender,sender_kind) VALUES(?,?,?,?,'user',?,?,?,?) ON CONFLICT(id) DO NOTHING", message.ID, link, assistant, request, text, time.Now().UnixMilli(), message.From.AgentID, message.From.Kind)
	if e == nil {
		_, _ = s.integrationDB.ExecContext(ctx, "UPDATE agent_link_chat_sessions SET revision=revision+1 WHERE link_id=? AND assistant_id=?", link, assistant)
		s.notifyMCPConnections()
		s.wakeTasks()
	}
	return protocol.SendMessageAccepted{MessageID: message.ID, Duplicate: accepted.Receipt.Duplicate}, e
}
func (s *Service) syncPotMCPChats(ctx context.Context, c PotMCPConnection) {
	rows, e := s.integrationDB.QueryContext(ctx, "SELECT id,assistant_id FROM agent_link_chat WHERE link_id=? AND role='user' ORDER BY created_at", c.ID)
	if e != nil {
		return
	}
	type original struct{ id, assistant string }
	var originals []original
	for rows.Next() {
		var item original
		if rows.Scan(&item.id, &item.assistant) != nil {
			rows.Close()
			return
		}
		originals = append(originals, item)
	}
	rows.Close()
	for _, item := range originals {
		var page MCPAccessMessagePage
		input := MCPAccessMessageRead{MessageID: item.id, After: 0}
		if c.Inbound {
			a, caller, err := s.localPotMCPUICaller(ctx, c, item.assistant)
			if err != nil {
				continue
			}
			page, e = a.messages(ctx, caller, input)
		} else {
			e = s.potMCPCall(ctx, c.ID, "pot_message_read", input, &page)
		}
		if e != nil {
			continue
		}
		for _, record := range page.Messages {
			message := record.Message
			if message.From != agentlink.AssistantTarget(c.RemoteNodeID, item.assistant) || message.ReplyTo == nil || *message.ReplyTo != item.id {
				continue
			}
			result, e := s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_chat(id,link_id,assistant_id,request_id,role,text,created_at,reply_to) VALUES(?,?,?,?,'assistant',?,?,?) ON CONFLICT(id) DO NOTHING", message.ID, c.ID, item.assistant, message.ID, message.Text, time.Now().UnixMilli(), item.id)
			if e == nil {
				if n, _ := result.RowsAffected(); n == 1 {
					_, _ = s.integrationDB.ExecContext(ctx, "UPDATE agent_link_chat_sessions SET revision=revision+1 WHERE link_id=? AND assistant_id=?", c.ID, item.assistant)
					s.notifyMCPConnections()
					s.publishReply("agent-link:"+c.ID+":"+item.assistant, protocol.Message{ID: message.ID, Text: message.Text})
				}
			}
		}
	}
}
