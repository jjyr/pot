package control

import (
	"context"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func (s *Service) localPotMCPAccess() *MCPAccess { return s.mcpAccess.Load() }
func (s *Service) inboundPotMCPConnections(ctx context.Context) ([]PotMCPConnection, error) {
	a := s.localPotMCPAccess()
	if a == nil {
		return nil, nil
	}
	rows, e := s.Tasks.DB().QueryContext(ctx, "SELECT connection_id,registration FROM mcp_receiver_endpoints ORDER BY connection_id,address")
	if e != nil {
		return nil, e
	}
	grouped := map[string]*PotMCPConnection{}
	var order []string
	for rows.Next() {
		var id, raw string
		var registration agentlink.MCPReceiverRegistration
		if e = rows.Scan(&id, &raw); e != nil {
			rows.Close()
			return nil, e
		}
		if e = mcpRead(raw, &registration); e != nil {
			rows.Close()
			return nil, e
		}
		c := grouped[id]
		if c == nil {
			node := registration.Receiver.Address.NodeID
			c = &PotMCPConnection{ID: id, NodeID: s.options.RuntimeID, RemoteNodeID: node, State: "active", Status: "offline", Inbound: true, Directory: &agentlink.Directory{NodeID: node, Name: registration.Receiver.Name, Adapter: registration.Receiver.Adapter, Targets: []agentlink.Endpoint{}}}
			grouped[id] = c
			order = append(order, id)
		}
		c.Directory.Targets = append(c.Directory.Targets, agentlink.Endpoint{Identity: registration.Receiver, Capabilities: registration.Capabilities})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	result := []PotMCPConnection{}
	for _, id := range order {
		c := grouped[id]
		if e = a.options.OAuth.CheckConnection(ctx, id); e != nil {
			c.State = "revoked"
			c.Status = "revoked"
		}
		if c.State == "active" && a.options.Queue.ReceiverOnline(id) {
			c.Status = "connected"
		}
		result = append(result, *c)
	}
	return result, nil
}
func (s *Service) inboundPotMCPConnection(ctx context.Context, id string) (PotMCPConnection, error) {
	connections, e := s.inboundPotMCPConnections(ctx)
	if e != nil {
		return PotMCPConnection{}, e
	}
	for _, c := range connections {
		if c.ID == id {
			return c, nil
		}
	}
	return PotMCPConnection{}, agentlink.ErrNotFound
}
func (s *Service) localPotMCPCaller(ctx context.Context, id string, actor protocol.AgentRef) (*MCPAccess, agentlink.MCPCaller, error) {
	var empty agentlink.MCPCaller
	a := s.localPotMCPAccess()
	if a == nil || actor.NodeID != s.options.RuntimeID || actor.Kind != "assistant" {
		return nil, empty, agentlink.ErrUnauthorized
	}
	if _, e := s.agentLinkIdentity(ctx, actor.AgentID); e != nil {
		return nil, empty, e
	}
	c, e := s.inboundPotMCPConnection(ctx, id)
	if e != nil {
		return nil, empty, e
	}
	if c.State != "active" {
		return nil, empty, agentlink.ErrUnauthorized
	}
	caller, e := agentlink.NewMCPNodeCaller(id, s.options.RuntimeID, actor, nil)
	return a, caller, e
}

func (s *Service) localPotMCPBusinessCaller(ctx context.Context, c PotMCPConnection, actor protocol.AgentRef) (*MCPAccess, agentlink.MCPCaller, error) {
	if actor.Kind == "user" && actor.NodeID == s.options.RuntimeID {
		a := s.localPotMCPAccess()
		if a == nil || !c.Inbound || c.State != "active" {
			return nil, agentlink.MCPCaller{}, agentlink.ErrUnauthorized
		}
		caller, e := agentlink.NewMCPCaller(c.ID, actor)
		return a, caller, e
	}
	return s.localPotMCPCaller(ctx, c.ID, actor)
}
