package agentlink

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
	"time"
)

// ApprovalEvent reports a committed human-decision request only to the original
// delegating node. It carries no tool parameters, decision token or authority.
type ApprovalEvent struct {
	To        protocol.AgentRef `json:"to"`
	ID        string            `json:"id"`
	JobID     string            `json:"job_id"`
	RequestID string            `json:"request_id"`
}

func (s *Service) PublishApproval(ctx context.Context, e ApprovalEvent) error {
	if !s.enterRPC() {
		return ErrUnavailable
	}
	defer s.rpcWG.Done()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	connections, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, c := range connections {
		if c.PeerHost.NodeID == e.To.NodeID && c.State != "revoked" && c.State != "disconnected" {
			var accepted struct{}
			return s.callConnection(ctx, c, "approval.event", e, &accepted)
		}
	}
	return ErrDisconnected
}
