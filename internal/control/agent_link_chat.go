package control

import (
	"context"
	"database/sql"
	"errors"
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
func (s *Service) SendLinkChat(ctx context.Context, link, assistant, request, text string) (any, error) {
	if !core.Identifier(request) || strings.TrimSpace(text) == "" || len(text) > protocol.MaxMailText {
		return nil, agentlink.ErrInvalid
	}
	c, to, e := s.linkChatTarget(ctx, link, assistant)
	if e != nil {
		return nil, agentLinkError(e)
	}
	if c.State == "disconnected" {
		return nil, agentLinkError(agentlink.ErrDisconnected)
	}
	var from protocol.AgentRef
	id, _ := core.HashJSON([]string{"agent-link-chat", link, assistant, request})
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	var old string
	e = s.integrationDB.QueryRowContext(ctx, "SELECT text,sender FROM agent_link_chat WHERE id=?", id).Scan(&old, &from.AgentID)
	duplicate := e == nil
	if duplicate && old != text {
		return nil, agentLinkError(agentlink.ErrConflict)
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if !duplicate {
		d, err := (potLinkBackend{s}).Directory(ctx)
		if err != nil {
			return nil, err
		}
		if d.DefaultAssistantID == "" {
			return nil, agentlink.ErrNotFound
		}
		from.AgentID = d.DefaultAssistantID
		_, e = s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_chat(id,link_id,assistant_id,request_id,role,text,created_at,sender) VALUES(?,?,?,?,?,?,?,?)", id, link, assistant, request, "user", text, time.Now().UnixMilli(), from.AgentID)
		if e != nil {
			return nil, e
		}
	}
	_, e = s.Mailbox.Send(ctx, id, protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "assistant", AgentID: from.AgentID}, protocol.SendMail{To: to.Address, Text: text}, nil)
	if e != nil {
		return nil, e
	}
	s.wakeTasks()
	s.Changes.Notify("agent-links")
	return map[string]any{"message_id": id, "duplicate": duplicate}, nil
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
	_, e = s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_chat(id,link_id,assistant_id,request_id,role,text,created_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING", message.ID, link, assistant, message.ID, "assistant", message.Text, time.Now().UnixMilli())
	if e != nil {
		return e
	}
	s.Changes.Notify("agent-links")
	s.Changes.Notify("assistants")
	return nil
}
func (s *Service) LinkChat(ctx context.Context, link, assistant, request string) (any, error) {
	conversation := linkChatID(link, assistant)
	if request != "" {
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
			return nil, e
		}
		return map[string]any{"runtime_id": s.options.RuntimeID, "owner_id": "agent-link:" + link + ":" + assistant, "request_id": request, "conversation_id": conversation, "state": state, "message_id": id}, nil
	}
	rows, e := s.integrationDB.QueryContext(ctx, "SELECT rowid,id,request_id,role,text,created_at FROM agent_link_chat WHERE link_id=? AND assistant_id=? ORDER BY rowid", link, assistant)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []protocol.UIMessage{}
	var cursor int64
	for rows.Next() {
		var sequence, created int64
		var id, request, role, text string
		if e = rows.Scan(&sequence, &id, &request, &role, &text, &created); e != nil {
			return nil, e
		}
		if sequence > cursor {
			cursor = sequence
		}
		items = append(items, protocol.UIMessage{Message: protocol.Message{ID: id, ConversationID: conversation, Role: protocol.Role(role), Text: text, Status: protocol.Complete, CreatedAtMS: created, RequestID: &request}, Sequence: strconv.FormatInt(sequence, 10), Version: sequence})
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return protocol.UIFrame{Kind: "reset", UISnapshot: protocol.UISnapshot{Snapshot: protocol.Snapshot{APIVersion: protocol.APIVersion, BootID: s.options.BootID, Cursor: cursor, ConversationID: conversation, Messages: []protocol.Message{}, Agent: protocol.AgentStatus{Phase: "idle", Model: ""}}, Messages: items}}, nil
}

func (s *Service) isLinkChatReply(ctx context.Context, m protocol.Envelope) (bool, error) {
	if m.ReplyTo == nil {
		return false, nil
	}
	var link, assistant string
	e := s.integrationDB.QueryRowContext(ctx, "SELECT link_id,assistant_id FROM agent_link_chat WHERE id=? AND role='user'", *m.ReplyTo).Scan(&link, &assistant)
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
	if m.From.NodeID != c.PeerHost.NodeID || m.From.AgentID != assistant {
		return false, agentlink.ErrConflict
	}
	return true, nil
}
