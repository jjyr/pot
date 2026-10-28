package control

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func operationBody(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAgentLinkTaskConfigurationUsesSerializableOwnerCapability(t *testing.T) {
	t.Parallel()
	b, _ := linkBackendFixture(t)
	ctx := context.Background()
	request := protocol.CreateTask{RequestID: "configured-task", Text: "Configured task\nUse the existing owner model."}
	input := agentlink.TaskRequest{From: protocol.AgentRef{NodeID: "node", Kind: "assistant", AgentID: "main"}, Target: agentlink.AssistantTarget(b.s.options.RuntimeID, "main"), RequestID: request.RequestID, Text: request.Text, Configuration: &request}
	raw := operationBody(t, input)
	var decoded agentlink.TaskRequest
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Configuration == nil {
		t.Fatal("configuration disappeared across serialization", string(raw), err)
	}
	accepted, err := (potLinkBackend{b.s}).Submit(ctx, decoded)
	if err != nil {
		t.Fatal(err)
	}
	state, err := (potLinkBackend{b.s}).Status(ctx, input.From, accepted.ID)
	if err != nil || state.ID != accepted.ID {
		t.Fatal(state, err)
	}
	for _, kind := range []string{"user", "task"} {
		forged := decoded
		forged.From.Kind = kind
		if _, err = (potLinkBackend{b.s}).Submit(ctx, forged); err == nil {
			t.Fatal("non-owner acquired configuration capability", kind)
		}
	}
	directory, err := b.Directory(ctx)
	if err != nil || directory.Capabilities(agentlink.AssistantTarget(directory.NodeID, "main")).Supports(agentlink.TaskConfigure) {
		t.Fatal("owner capability leaked to peers", directory, err)
	}
}

func TestAgentLinkUserTaskReceiptRetainsFullCallerScope(t *testing.T) {
	t.Parallel()
	b, _ := linkBackendFixture(t)
	ctx := context.Background()
	from := protocol.AgentRef{NodeID: "remote-user-node", Kind: "user", AgentID: "session"}
	task, err := b.Submit(ctx, agentlink.TaskRequest{From: from, RequestID: "request", Text: "Work\nKeep caller identity", Target: agentlink.AssistantTarget(b.s.options.RuntimeID, "main")})
	if err != nil {
		t.Fatal(err)
	}
	for _, other := range []protocol.AgentRef{{NodeID: from.NodeID, Kind: "assistant", AgentID: from.AgentID}, {NodeID: "another-node", Kind: from.Kind, AgentID: from.AgentID}, {NodeID: from.NodeID, Kind: from.Kind, AgentID: "another-session"}} {
		if _, err = b.Status(ctx, other, task.ID); err == nil {
			t.Fatal("task crossed caller scope", other)
		}
		if _, err = b.Cancel(ctx, other, task.ID); err == nil {
			t.Fatal("task cancellation crossed caller scope", other)
		}
	}
	if _, err = b.Status(ctx, from, task.ID); err != nil {
		t.Fatal(err)
	}
}
