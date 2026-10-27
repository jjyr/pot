package control

import (
	"context"

	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func (s *Service) syncPotMCPMail(ctx context.Context, c PotMCPConnection, client *mcpreceiver.Client) error {
	messages, e := s.Mailbox.PendingNode(ctx, s.options.RuntimeID, c.RemoteNodeID)
	if e != nil {
		return e
	}
	for _, message := range messages {
		if message.From.Kind != "assistant" || message.From.NodeID != s.options.RuntimeID {
			return agentlink.ErrUnauthorized
		}
		if _, e = s.agentLinkIdentity(ctx, message.From.AgentID); e != nil {
			return e
		}
		if message.To.Kind == "user" {
			if message.ReplyTo == nil {
				return agentlink.ErrUnauthorized
			}
		} else if c.Directory == nil || !c.Directory.Capabilities(message.To).Supports(agentlink.ChatSend) {
			return agentlink.ErrUnsupported
		}
		input := agentlink.MCPNodeMail{Message: message}
		if message.ReplyTo != nil {
			input.ProcessingParentID = *message.ReplyTo
		}
		var receipt MCPAccessMessageAcceptance
		if e = client.Call(ctx, "pot_receiver_node_mail", input, &receipt); e != nil {
			return e
		}
		if e = s.Mailbox.Acknowledge(ctx, receipt.Message); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) syncInboundPotMCPMail(ctx context.Context) {
	a := s.localPotMCPAccess()
	if a == nil {
		return
	}
	connections, e := s.inboundPotMCPConnections(ctx)
	if e != nil {
		return
	}
	for _, c := range connections {
		if c.State != "active" {
			continue
		}
		s.syncPotMCPChats(ctx, c)
		messages, e := s.Mailbox.PendingNode(ctx, s.options.RuntimeID, c.RemoteNodeID)
		if e != nil {
			continue
		}
		for _, message := range messages {
			if e = a.enqueueLocalMCPMail(ctx, c.ID, message); e != nil {
				break
			}
			// Queue acceptance is a durable transfer receipt, not model processing.
			if e = s.Mailbox.Acknowledge(ctx, protocol.Receipt{Message: message}); e != nil {
				break
			}
		}
	}
}
