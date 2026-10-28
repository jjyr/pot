package control

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

type agentLinkHost struct{ service *agentlink.History }

type controlLinkMailbox struct{ s *Service }

func (m controlLinkMailbox) PendingNode(ctx context.Context, local, peer string) ([]protocol.Envelope, error) {
	return m.s.Mailbox.PendingNode(ctx, local, peer)
}
func (m controlLinkMailbox) Deliver(ctx context.Context, value protocol.Envelope) (protocol.Receipt, error) {
	direct, err := m.s.isLinkChatReply(ctx, value)
	if err != nil {
		return protocol.Receipt{}, err
	}
	if !direct {
		if value.To.Kind != "assistant" {
			return protocol.Receipt{}, agentlink.ErrNotFound
		}
	}
	var receipt protocol.Receipt
	if direct {
		receipt, err = m.s.Mailbox.DeliverChat(ctx, value)
	} else {
		receipt, err = m.s.Mailbox.Deliver(ctx, value)
	}
	if err == nil && direct {
		err = m.s.captureLinkChat(ctx, value)
	}
	if err == nil {
		m.s.wakeTasks()
		m.s.Changes.Notify("assistant-mail")
	}
	return receipt, err
}
func (m controlLinkMailbox) Acknowledge(ctx context.Context, value protocol.Receipt) error {
	return m.s.Mailbox.Acknowledge(ctx, value)
}

func (s *Service) agentLinkIdentity(ctx context.Context, id string) (agentlink.Identity, error) {
	if id == "" {
		return agentlink.Identity{Address: protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "node", AgentID: s.options.RuntimeID}, Name: "Pot", Adapter: "pot"}, nil
	}
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil {
		return agentlink.Identity{}, err
	}
	for _, d := range definitions {
		if d.ID == id {
			return agentlink.Identity{Address: protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "assistant", AgentID: id}, Name: d.Name, Adapter: "pot"}, nil
		}
	}
	return agentlink.Identity{}, protocol.APIError{Code: "agent_link_assistant_unavailable"}
}

func (s *Service) openAgentLinks() error {
	s.agentLinks = &agentLinkHost{service: &agentlink.History{DB: s.integrationDB}}
	if err := s.restoreChatReplyBindings(s.ctx); err != nil {
		return err
	}
	s.pluginWG.Add(1)
	go func() {
		defer s.pluginWG.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.syncTaskExecutions(s.ctx)
			}
		}
	}()
	return nil
}
func (s *Service) closeAgentLinks() error { return nil }

func (s *Service) AgentLinks(ctx context.Context) (any, error) {
	if s.agentLinks == nil {
		return nil, protocol.APIError{Code: "agent_link_unavailable"}
	}
	identity, _ := s.agentLinkIdentity(ctx, "")
	h := s.agentLinks
	self := map[string]any{"id": identity.Address.AgentID, "node_id": s.options.RuntimeID, "kind": "pot", "name": identity.Name, "status": "ready"}
	localObservation, observationErr := (potLinkBackend{s}).Observe(ctx, "")
	localSample := agentlink.ObservationSample{ObservedAtMS: time.Now().UnixMilli()}
	if observationErr == nil {
		localSample.Value = &localObservation
	} else {
		localSample.Error = "agent_link_observation_unavailable"
	}
	self["observation"] = localSample
	connections, err := h.service.List(ctx)
	if err != nil {
		return nil, err
	}
	items := []any{}
	for _, c := range connections {
		if c.State == "revoked" {
			continue
		}
		state := "unsupported"
		peerName := c.Peer.Name
		localOutbox := agentlink.MessageQueue{}
		localOutbox.Pending, localOutbox.OldestAtMS, err = inbox.OutboxStats(ctx, s.Tasks.DB(), "out", s.options.RuntimeID, c.Peer.Address.NodeID)
		if err != nil {
			return nil, err
		}
		assistants := []any{}
		if c.Directory != nil {
			for _, a := range c.Directory.Assistants() {
				var count int64
				var replyID, preview string
				var at int64
				if e := s.integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_link_chat WHERE link_id=? AND assistant_id=? AND role='assistant'", c.ID, a.Address.AgentID).Scan(&count); e != nil {
					return nil, e
				}
				e := s.integrationDB.QueryRowContext(ctx, "SELECT id,text,created_at FROM agent_link_chat WHERE link_id=? AND assistant_id=? AND role='assistant' ORDER BY rowid DESC LIMIT 1", c.ID, a.Address.AgentID).Scan(&replyID, &preview, &at)
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return nil, e
				}
				capabilities, err := s.AgentCapabilities(ctx, "agent-link:"+c.ID+":"+a.Address.AgentID)
				if err != nil {
					return nil, err
				}
				assistants = append(assistants, map[string]any{"address": a.Address, "name": a.Name, "avatar": a.Avatar, "adapter": a.Adapter, "capabilities": capabilities, "reply_count": count, "reply_id": replyID, "preview": preview, "last_reply_at_ms": at})
			}
			peerName = c.Directory.Name
		}
		lastExchange := int64(0)
		if c.LastSeen != nil {
			lastExchange = c.LastSeen.UnixMilli()
		}
		items = append(items, map[string]any{"assistants": assistants, "directory": c.Directory, "id": c.ID, "peer": map[string]any{"id": c.Peer.Address.AgentID, "node_id": c.Peer.Address.NodeID, "kind": c.Peer.Adapter, "name": peerName, "address": c.Endpoint}, "status": state, "revision": c.CreatedAt.UnixMilli(), "last_known_state": c.LastKnownState, "last_exchange_at_ms": lastExchange, "outbox": localOutbox, "observation": agentlink.ObservationSample{}})
	}
	mcpConnections, e := s.PotMCPConnections(ctx)
	if e != nil {
		return nil, e
	}
	incoming, e := s.inboundPotMCPConnections(ctx)
	if e != nil {
		return nil, e
	}
	mcpConnections = append(mcpConnections, incoming...)
	for _, connection := range mcpConnections {
		assistants := []any{}
		if connection.Directory != nil {
			for _, endpoint := range connection.Directory.Assistants() {
				assistants = append(assistants, map[string]any{"address": endpoint.Address, "name": endpoint.Name, "avatar": endpoint.Avatar, "adapter": endpoint.Adapter, "capabilities": endpoint.Capabilities})
			}
		}
		name := connection.RemoteNodeID
		if connection.Directory != nil {
			name = connection.Directory.Name
		}
		kind := ""
		if connection.Directory != nil {
			kind = connection.Directory.Adapter
		}
		items = append(items, map[string]any{"id": connection.ID, "transport": "mcp", "inbound": connection.Inbound, "peer": map[string]any{"node_id": connection.RemoteNodeID, "kind": kind, "name": name, "address": connection.URL}, "assistants": assistants, "directory": connection.Directory, "status": connection.Status, "revision": connection.Revision, "last_error": connection.LastError, "last_exchange_at_ms": connection.LastSeenAt})
	}
	return map[string]any{"self": self, "items": items}, nil
}

func agentLinkError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{agentlink.ErrModelUnavailable, agentlink.ErrWorkspaceUnavailable, agentlink.ErrAssistantBusy, agentlink.ErrAssistantChanged, agentlink.ErrInvalid, agentlink.ErrNotFound, agentlink.ErrUnauthorized, agentlink.ErrConflict, agentlink.ErrRevoked, agentlink.ErrUnavailable, agentlink.ErrUnsupported, agentlink.ErrDisconnected, agentlink.ErrCapacity, agentlink.ErrFenced} {
		if errors.Is(err, known) {
			return protocol.APIError{Code: known.Error()}
		}
	}
	var api protocol.APIError
	if errors.As(err, &api) {
		return err
	}
	return protocol.APIError{Code: "agent_link_failed"}
}

func (s *Service) linkedPeer(ctx context.Context, local, peer protocol.AgentRef) bool {
	if local.NodeID == s.options.RuntimeID {
		if c, found, e := s.potMCPRoute(ctx, peer); e == nil && found {
			return c.State == "active" && c.Directory != nil && c.Directory.Capabilities(peer).Supports(agentlink.ChatSend)
		}
	}
	return false
}

func (s *Service) agentLinkPeers(ctx context.Context) ([]protocol.MailAssistant, error) {
	result := []protocol.MailAssistant{}
	connections, e := s.PotMCPConnections(ctx)
	if e != nil {
		return nil, e
	}
	incoming, e := s.inboundPotMCPConnections(ctx)
	if e != nil {
		return nil, e
	}
	for _, c := range append(connections, incoming...) {
		if c.State != "active" || c.Directory == nil {
			continue
		}
		for _, a := range c.Directory.Assistants() {
			if a.Capabilities.Supports(agentlink.ChatSend) {
				result = append(result, protocol.MailAssistant{Address: a.Address, Name: a.Name})
			}
		}
	}
	return result, nil
}

// ManageLinkedAssistant is an operator entry point, never an agent tool.
func (s *Service) ManageLinkedAssistant(ctx context.Context, link string, input agentlink.AssistantManagementRequest) (agentlink.AssistantManagementResult, error) {
	c, e := s.inboundPotMCPConnection(ctx, link)
	if e != nil {
		return agentlink.AssistantManagementResult{}, e
	}
	value, e := s.nativeUserCall(ctx, link, agentlink.NodeTarget(c.RemoteNodeID), nil, nil, &input)
	if e != nil {
		return agentlink.AssistantManagementResult{}, agentLinkError(e)
	}
	return value.(agentlink.AssistantManagementResult), nil
}
func (s *Service) ManageHumanLinkedAssistant(ctx context.Context, link string, input agentlink.AssistantManagementRequest) (agentlink.AssistantManagementResult, error) {
	return s.ManageLinkedAssistant(context.WithValue(ctx, nativeHumanKey{}, true), link, input)
}
