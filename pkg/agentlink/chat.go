package agentlink

import (
	"context"
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

// ChatEndpoint is the typed access contract shared by every product chat.
// Resolution chooses an adapter; consumers never infer behavior from location.
// Receipts describe acceptance, Read/Subscribe describe execution and output.
type ChatEndpoint interface {
	Read(context.Context) (protocol.UIFrame, error)
	Send(context.Context, protocol.SendMessage) (protocol.SendMessageAccepted, error)
	Receipt(context.Context, protocol.SendReceiptQuery) (protocol.SendReceipt, error)
	Subscribe(context.Context) (Subscription, error)
}
