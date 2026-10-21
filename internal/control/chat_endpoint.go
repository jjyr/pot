package control

import (
	"context"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"sync"
	"time"
)

func (p agentPort) chatEndpoint(ctx context.Context, target agentlink.Target) (agentlink.ChatEndpoint, error) {
	if target.LinkID != "" {
		return linkedChatEndpoint{p.s, target}, nil
	}
	c, err := p.s.Conversation(ctx, target.AssistantID)
	return nativeChatEndpoint{c}, err
}

var _ agentlink.ChatEndpoint = nativeChatEndpoint{}
var _ agentlink.ChatEndpoint = linkedChatEndpoint{}

type nativeChatEndpoint struct{ c *assistant.Conversation }

func (a nativeChatEndpoint) Read(ctx context.Context) (protocol.UIFrame, error) {
	return a.c.UIFrame(ctx, 0, true)
}
func (a nativeChatEndpoint) Send(ctx context.Context, input protocol.SendMessage) (protocol.SendMessageAccepted, error) {
	return a.c.Send(ctx, input)
}
func (a nativeChatEndpoint) Receipt(ctx context.Context, query protocol.SendReceiptQuery) (protocol.SendReceipt, error) {
	return a.c.SendReceipt(ctx, query)
}
func (a nativeChatEndpoint) Subscribe(ctx context.Context) (agentlink.Subscription, error) {
	updates, close, err := a.c.SubscribeUI(ctx)
	return agentlink.Subscription{Updates: updates, Frame: a.c.UIFrame, Close: close}, err
}

type linkedChatEndpoint struct {
	s      *Service
	target agentlink.Target
}

func (a linkedChatEndpoint) Read(ctx context.Context) (protocol.UIFrame, error) {
	v, err := a.s.LinkChat(ctx, a.target.LinkID, a.target.AssistantID)
	if err != nil {
		return protocol.UIFrame{}, err
	}
	return v, nil
}
func (a linkedChatEndpoint) Send(ctx context.Context, input protocol.SendMessage) (protocol.SendMessageAccepted, error) {
	if len(input.Attachments) != 0 || len(input.References) != 0 {
		return protocol.SendMessageAccepted{}, agentlink.ErrUnsupported
	}
	if input.ConversationID != linkChatID(a.target.LinkID, a.target.AssistantID) {
		return protocol.SendMessageAccepted{}, agentlink.ErrConflict
	}
	return a.s.SendLinkChat(ctx, a.target.LinkID, a.target.AssistantID, input.RequestID, input.Text)
}
func (a linkedChatEndpoint) Receipt(ctx context.Context, query protocol.SendReceiptQuery) (protocol.SendReceipt, error) {
	if query.ConversationID != linkChatID(a.target.LinkID, a.target.AssistantID) {
		return protocol.SendReceipt{}, agentlink.ErrConflict
	}
	return a.s.linkChatReceipt(ctx, a.target.LinkID, a.target.AssistantID, query.RequestID)
}
func (a linkedChatEndpoint) Subscribe(ctx context.Context) (agentlink.Subscription, error) {
	updates := make(chan struct{}, 1)
	updates <- struct{}{}
	done := make(chan struct{})
	var once sync.Once
	closeSubscription := func() { once.Do(func() { close(done) }) }
	go func() {
		defer close(updates)
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-a.s.ctx.Done():
				return
			case <-ticker.C:
				select {
				case updates <- struct{}{}:
				default:
				}
			}
		}
	}()
	return agentlink.Subscription{Updates: updates, Frame: func(ctx context.Context, _ int64, _ bool) (protocol.UIFrame, error) { return a.Read(ctx) }, Close: closeSubscription}, nil
}
