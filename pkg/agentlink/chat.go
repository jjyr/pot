package agentlink

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// ChatLookup binds a status read to the original caller, target and input.
// It grants neither another conversation's history nor owner administration.
type ChatLookup = protocol.ChatLookup
type ChatStatus = protocol.ChatStatus

// ChatBackend is implemented only by receivers that offer direct user chats.
// Assistant messages and delegated tasks do not require this operation.
type ChatBackend interface {
	ChatStatus(context.Context, ChatLookup) (ChatStatus, error)
}

func (s *Service) ChatStatus(ctx context.Context, link string, query ChatLookup) (ChatStatus, error) {
	if !s.enterRPC() {
		return ChatStatus{}, ErrUnavailable
	}
	defer s.rpcWG.Done()
	if !query.From.Valid() || query.From.Kind != "user" || query.From.NodeID != s.options.HostIdentity.NodeID || !core.Identifier(query.AssistantID) || !core.Identifier(query.MessageID) {
		return ChatStatus{}, ErrUnauthorized
	}

	var result ChatStatus
	node := s.options.HostIdentity.NodeID
	var err error
	if link == "" {
		backend, ok := s.options.Backend.(ChatBackend)
		if !ok {
			return result, ErrUnsupported
		}
		result, err = backend.ChatStatus(ctx, query)
	} else {
		c, e := s.Get(ctx, link)
		if e != nil {
			return result, e
		}
		node = c.PeerHost.NodeID
		err = s.callConnection(ctx, c, "chat.status", query, &result)
	}
	if err != nil {
		return result, err
	}
	return result, validateChatStatus(result, query, node)
}

func validateChatStatus(result ChatStatus, query ChatLookup, node string) error {
	if !result.ChatReply.Valid() || result.InputID != query.MessageID || len(result.Messages) > protocol.MailPageSize {
		return ErrConflict
	}
	seen := map[string]bool{}
	for _, m := range result.Messages {
		if m.Validate() != nil || m.From.NodeID != node || m.From.Kind != "assistant" || m.From.AgentID != query.AssistantID || m.To != query.From || m.ReplyTo == nil || *m.ReplyTo != query.MessageID || seen[m.ID] {
			return ErrConflict
		}
		seen[m.ID] = true
	}
	return nil
}

func (s *Service) chatBusiness(ctx context.Context, caller string, raw json.RawMessage) (ChatStatus, error) {
	var query ChatLookup
	if decodeJSON(raw, &query) != nil || !query.From.Valid() || query.From.Kind != "user" || query.From.NodeID != caller || !core.Identifier(query.MessageID) || !core.Identifier(query.AssistantID) {
		return ChatStatus{}, ErrUnauthorized
	}
	backend, ok := s.options.Backend.(ChatBackend)
	if !ok {
		return ChatStatus{}, ErrUnsupported
	}
	result, err := backend.ChatStatus(ctx, query)
	if err != nil {
		return result, err
	}
	return result, validateChatStatus(result, query, s.options.HostIdentity.NodeID)
}

// ChatEndpoint is the typed access contract shared by every product chat.
// Resolution chooses an adapter; consumers never infer behavior from location.
// Receipts describe acceptance, Read/Subscribe describe execution and output.
type ChatEndpoint interface {
	Read(context.Context) (protocol.UIFrame, error)
	Send(context.Context, protocol.SendMessage) (protocol.SendMessageAccepted, error)
	Receipt(context.Context, protocol.SendReceiptQuery) (protocol.SendReceipt, error)
	Subscribe(context.Context) (Subscription, error)
}
