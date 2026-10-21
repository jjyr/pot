package control

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jjyr/pot/pkg/storage"
	"strconv"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func linkChatID(link, assistant string) string {
	id, _ := core.HashJSON([]string{"agent-link-chat", link, assistant})
	return id
}
func (s *Service) linkChatTarget(ctx context.Context, link, assistant string) (agentlink.Connection, agentlink.Identity, error) {
	c, e := s.agentLinks.service.Get(ctx, link)
	if e != nil {
		return c, agentlink.Identity{}, e
	}
	if c.State == "revoked" {
		return c, agentlink.Identity{}, agentlink.ErrRevoked
	}
	if c.Directory == nil {
		return c, agentlink.Identity{}, agentlink.ErrUnavailable
	}
	for _, a := range c.Directory.Assistants {
		if a.Address.AgentID == assistant {
			return c, a, nil
		}
	}
	return c, agentlink.Identity{}, agentlink.ErrNotFound
}
func (s *Service) SendLinkChat(ctx context.Context, link, assistant, request, text string) (protocol.SendMessageAccepted, error) {
	if !core.Identifier(request) || strings.TrimSpace(text) == "" || len(text) > protocol.MaxMailText {
		return protocol.SendMessageAccepted{}, agentlink.ErrInvalid
	}
	c, to, e := s.linkChatTarget(ctx, link, assistant)
	if e != nil {
		return protocol.SendMessageAccepted{}, agentLinkError(e)
	}
	if c.State == "disconnected" {
		return protocol.SendMessageAccepted{}, agentLinkError(agentlink.ErrDisconnected)
	}
	var from protocol.AgentRef
	id, _ := core.HashJSON([]string{"agent-link-chat", link, assistant, request})
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	var old string
	e = s.integrationDB.QueryRowContext(ctx, "SELECT text,sender,sender_kind FROM agent_link_chat WHERE id=?", id).Scan(&old, &from.AgentID, &from.Kind)
	duplicate := e == nil
	if duplicate && old != text {
		return protocol.SendMessageAccepted{}, agentLinkError(agentlink.ErrConflict)
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return protocol.SendMessageAccepted{}, e
	}
	if !duplicate {
		sessionID := linkChatID(link, assistant)
		_, e = s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_chat_sessions(link_id,assistant_id,id) VALUES(?,?,?) ON CONFLICT DO NOTHING", link, assistant, sessionID)
		if e != nil {
			return protocol.SendMessageAccepted{}, e
		}
		e = s.integrationDB.QueryRowContext(ctx, "SELECT id FROM agent_link_chat_sessions WHERE link_id=? AND assistant_id=?", link, assistant).Scan(&from.AgentID)
		if e != nil {
			return protocol.SendMessageAccepted{}, e
		}
		from.Kind = "user"
		_, e = s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_chat(id,link_id,assistant_id,request_id,role,text,created_at,sender,sender_kind) VALUES(?,?,?,?,?,?,?,?,?)", id, link, assistant, request, "user", text, time.Now().UnixMilli(), from.AgentID, from.Kind)
		if e != nil {
			return protocol.SendMessageAccepted{}, e
		}
		_, e = s.integrationDB.ExecContext(ctx, "UPDATE agent_link_chat_sessions SET revision=revision+1 WHERE link_id=? AND assistant_id=?", link, assistant)
		if e != nil {
			return protocol.SendMessageAccepted{}, e
		}
	}
	_, e = s.Mailbox.Send(ctx, id, protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: from.Kind, AgentID: from.AgentID}, protocol.SendMail{To: to.Address, Text: text}, nil)
	if e != nil {
		return protocol.SendMessageAccepted{}, e
	}
	s.wakeTasks()
	s.Changes.Notify("agent-links")
	return protocol.SendMessageAccepted{MessageID: id, Duplicate: duplicate}, nil
}

// captureLinkChat routes replies to a direct user conversation, keeping peer
// correspondence out of the local assistant's autonomous mail consumer.
func (s *Service) captureLinkChat(ctx context.Context, message protocol.Envelope) error {
	if message.ReplyTo == nil {
		return nil
	}
	var link, assistant string
	e := s.integrationDB.QueryRowContext(ctx, "SELECT link_id,assistant_id FROM agent_link_chat WHERE id=? AND role='user'", *message.ReplyTo).Scan(&link, &assistant)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	c, e := s.agentLinks.service.Get(ctx, link)
	if e != nil {
		return e
	}
	if message.From.NodeID != c.PeerHost.NodeID || message.From.AgentID != assistant {
		return agentlink.ErrConflict
	}
	e = storage.Transaction(ctx, s.integrationDB, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "INSERT INTO agent_link_chat(id,link_id,assistant_id,request_id,role,text,created_at,reply_to) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET reply_to=excluded.reply_to WHERE agent_link_chat.reply_to=''", message.ID, link, assistant, message.ID, "assistant", message.Text, time.Now().UnixMilli(), *message.ReplyTo)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 0 {
			_, err = tx.ExecContext(ctx, "UPDATE agent_link_chat_sessions SET revision=revision+1 WHERE link_id=? AND assistant_id=?", link, assistant)
		}
		return err
	})
	if e != nil {
		return e
	}
	s.Changes.Notify("agent-links")
	s.Changes.Notify("assistants")
	return nil
}
func (s *Service) linkChatReceipt(ctx context.Context, link, assistant, request string) (protocol.SendReceipt, error) {
	conversation := linkChatID(link, assistant)
	var id string
	e := s.integrationDB.QueryRowContext(ctx, "SELECT id FROM agent_link_chat WHERE link_id=? AND assistant_id=? AND request_id=? AND role='user'", link, assistant, request).Scan(&id)
	state := "accepted"
	if e == nil {
		var accepted bool
		e = s.Tasks.DB().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agent_messages WHERE id=? AND direction='out')", id).Scan(&accepted)
		if !accepted {
			state = "unconfirmed"
		}
	}
	if errors.Is(e, sql.ErrNoRows) {
		state = "unconfirmed"
	} else if e != nil {
		return protocol.SendReceipt{}, e
	}
	return protocol.SendReceipt{SendReceiptQuery: protocol.SendReceiptQuery{RequestID: request, ConversationID: conversation}, RuntimeID: s.options.RuntimeID, OwnerID: "agent-link:" + link + ":" + assistant, State: state, MessageID: id}, nil
}

func (s *Service) LinkChat(ctx context.Context, link, assistant string) (protocol.UIFrame, error) {
	conversation := linkChatID(link, assistant)

	var cursor int64 = 1
	err := s.integrationDB.QueryRowContext(ctx, "SELECT revision FROM agent_link_chat_sessions WHERE link_id=? AND assistant_id=?", link, assistant).Scan(&cursor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return protocol.UIFrame{}, err
	}
	rows, e := s.integrationDB.QueryContext(ctx, "SELECT rowid,id,request_id,role,text,created_at,reply_to,reply_state,reply_error,reply_uncertain FROM agent_link_chat WHERE link_id=? AND assistant_id=? ORDER BY rowid", link, assistant)
	if e != nil {
		return protocol.UIFrame{}, e
	}
	defer rows.Close()
	items := []protocol.UIMessage{}
	replies := []protocol.ChatReply{}

	for rows.Next() {
		var sequence, created int64
		var id, request, role, text, parent, state, detail string
		var uncertain bool
		if e = rows.Scan(&sequence, &id, &request, &role, &text, &created, &parent, &state, &detail, &uncertain); e != nil {
			return protocol.UIFrame{}, e
		}
		if role == "user" {
			replies = append(replies, protocol.ChatReply{InputID: id, State: state, Error: detail, Uncertain: uncertain})
		}
		items = append(items, protocol.UIMessage{Message: protocol.Message{ID: id, ConversationID: conversation, Role: protocol.Role(role), Text: text, Status: protocol.Complete, CreatedAtMS: created, RequestID: &request, ReplyTo: parent}, Sequence: strconv.FormatInt(sequence, 10), Version: cursor})
	}
	if e = rows.Err(); e != nil {
		return protocol.UIFrame{}, e
	}
	return protocol.UIFrame{Kind: "reset", UISnapshot: protocol.UISnapshot{Snapshot: protocol.Snapshot{APIVersion: protocol.APIVersion, BootID: s.options.BootID, Cursor: cursor, ConversationID: conversation, Messages: []protocol.Message{}, Agent: protocol.AgentStatus{Phase: protocol.Ready, Model: ""}}, Messages: items, Replies: replies}}, nil
}

func (s *Service) isLinkChatReply(ctx context.Context, m protocol.Envelope) (bool, error) {
	if m.ReplyTo == nil {
		return false, nil
	}
	var link, assistant, sender, senderKind string
	e := s.integrationDB.QueryRowContext(ctx, "SELECT link_id,assistant_id,sender,sender_kind FROM agent_link_chat WHERE id=? AND role='user'", *m.ReplyTo).Scan(&link, &assistant, &sender, &senderKind)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	c, e := s.agentLinks.service.Get(ctx, link)
	if e != nil {
		return false, e
	}
	if m.From.NodeID != c.PeerHost.NodeID || m.From.Kind != "assistant" || m.From.AgentID != assistant || m.To != (protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: senderKind, AgentID: sender}) {
		return false, agentlink.ErrConflict
	}
	return true, nil
}
