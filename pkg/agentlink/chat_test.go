package agentlink

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
)

type chatStatusBackend struct {
	*testBackend
	result ChatStatus
}

func (b *chatStatusBackend) ChatStatus(context.Context, ChatLookup) (ChatStatus, error) {
	return b.result, nil
}

func TestChatStatusValidatesEveryExecutionAdapter(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "chat-validator-a"), newFixture(t, "chat-validator-b")
	local := &chatStatusBackend{testBackend: &testBackend{f: a}}
	peer := &chatStatusBackend{testBackend: &testBackend{f: b}}
	a.service.options.Backend = local
	b.service.options.Backend = peer
	c := connect(t, a, b)
	query := ChatLookup{From: protocol.AgentRef{NodeID: a.id, Kind: "user", AgentID: "session"}, AssistantID: "assistant", MessageID: "input"}
	for _, result := range []ChatStatus{
		{ChatReply: protocol.ChatReply{InputID: "input", State: "running"}},
		{ChatReply: protocol.ChatReply{InputID: "other-input", State: "running"}},
		{ChatReply: protocol.ChatReply{InputID: "input", State: "invented"}},
	} {
		local.result = result
		peer.result = result
		for _, link := range []string{"", c.ID} {
			value, err := a.service.ChatStatus(ctx, link, query)
			if result.InputID == query.MessageID && result.State == "running" {
				if err != nil || value.State != "running" {
					t.Fatal(link, value, err)
				}
			} else if !errors.Is(err, ErrConflict) {
				t.Fatal("adapter bypassed validation", link, err)
			}
		}
	}
}
