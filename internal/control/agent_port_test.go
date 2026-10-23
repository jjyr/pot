package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestAgentPortEmptyNodeUserTaskResultAndRetrySurviveRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { batchReply(w, "remote-user-task-result") }))
	defer upstream.Close()
	a := agentControl(t, "user-task-source")
	defs, _ := a.service.Registry.Definitions(ctx)
	if err := a.service.DeleteAssistant(ctx, defs[0].ID, defs[0].Revision); err != nil {
		t.Fatal(err)
	}
	receiver := taskControl(t, "user-task-receiver", upstream.URL)
	b := &agentControlFixture{service: receiver, endpoint: controlPeerEndpoint(receiver)}
	c := connectAgentControls(t, a, b)
	owner := "agent-link:" + c.ID + ":main"
	description, err := a.service.AgentCapabilities(ctx, owner)
	if err != nil || !description.Supports(agentlink.TaskSubmit) || !description.Supports(agentlink.TaskList) || description.Supports(agentlink.TaskConfigure) || description.Supports(agentlink.Schedules) {
		t.Fatal(description, err)
	}
	request := protocol.CreateTask{RequestID: "user-task", Text: "Read report\nReturn a result to this user conversation."}
	call := agentlink.Call{Body: operationBody(t, request)}
	accepted, err := callAgentWhenReady(ctx, a.service, owner, agentlink.TaskSubmit, call)
	if err != nil {
		t.Fatal(err)
	}
	info := accepted.(protocol.TaskInfo)
	if info.AssistantID != owner {
		t.Fatal("task presentation lost its conversation", info)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		a.service.syncTaskExecutions(ctx)
		frame, e := a.service.LinkChat(ctx, c.ID, "main")
		if e != nil {
			t.Fatal(e)
		}
		messages := frame.Messages
		if len(messages) == 1 && messages[0].Text == "remote-user-task-result" {
			break
		}
		if time.Now().After(deadline) {
			record, readErr := a.service.Tasks.Get(ctx, linkChatID(c.ID, "main"), info.ID)
			t.Fatalf("task result did not reach user conversation: frame=%+v record=%+v execution=%+v err=%v", frame, record, nil, readErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	detail, err := callAgentWhenReady(ctx, a.service, owner, agentlink.TaskRead, agentlink.Call{ID: info.ID})
	if err != nil {
		t.Fatal(err)
	}
	details := detail.(map[string]any)
	if details["info"].(map[string]any)["assistant_id"] != owner || details["result"].(map[string]any)["text"] != "remote-user-task-result" {
		t.Fatal("detail lost directing conversation or result", detail)
	}
	delegation, err := a.service.Tasks.Execution(ctx, linkChatID(c.ID, "main"), info.ID)
	if err != nil || delegation.Execution.Request.From.Kind != "user" {
		t.Fatal(delegation, err)
	}
	var autonomous int
	if err = a.service.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_messages WHERE json_extract(envelope,'$.kind')='task_result'").Scan(&autonomous); err != nil || autonomous != 0 {
		t.Fatal("user task woke a local assistant", autonomous, err)
	}
	other := request
	other.Text = "Different content"
	if _, err = callAgentWhenReady(ctx, a.service, owner, agentlink.TaskSubmit, agentlink.Call{Body: operationBody(t, other)}); err == nil {
		t.Fatal("conflicting user retry accepted")
	}
	if _, err = callAgentWhenReady(ctx, a.service, owner, agentlink.ChatCancel, agentlink.Call{Body: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("unadvertised operation reached local chat")
	}
	options := a.service.options
	if err = a.service.Close(); err != nil {
		t.Fatal(err)
	}
	options.BootID = "user-task-restarted"
	a.service, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.service.agentLinks.service.Sync(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	repeated, err := callAgentWhenReady(ctx, a.service, owner, agentlink.TaskSubmit, call)
	if err != nil || repeated.(protocol.TaskInfo).ID != info.ID {
		t.Fatal(repeated, err)
	}
	a.service.syncTaskExecutions(ctx)
	frame, err := a.service.LinkChat(ctx, c.ID, "main")
	if err != nil || len(frame.Messages) != 1 {
		t.Fatal("result duplicated after restart", frame, err)
	}
	page, err := callAgentWhenReady(ctx, a.service, owner, agentlink.TaskList, agentlink.Call{Query: map[string]string{"scope": "all"}})
	if err != nil || len(page.(protocol.TaskPage).Items) != 1 || page.(protocol.TaskPage).Items[0].AssistantID != owner {
		t.Fatal(page, err)
	}
	if err = a.service.agentLinks.service.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = callAgentWhenReady(ctx, a.service, owner, agentlink.TaskRead, agentlink.Call{ID: info.ID}); err != nil {
		t.Fatal("link deletion hid retained task", err)
	}
	if _, err = callAgentWhenReady(ctx, a.service, owner, agentlink.ChatRead, agentlink.Call{}); err != nil {
		t.Fatal("link deletion hid retained conversation", err)
	}
	if repeated, err = callAgentWhenReady(ctx, a.service, owner, agentlink.TaskSubmit, call); err != nil || repeated.(protocol.TaskInfo).ID != info.ID {
		t.Fatal("revoked link hid original immutable acceptance", repeated, err)
	}
	newRequest := request
	newRequest.RequestID = "new-after-revocation"
	if _, err = callAgentWhenReady(ctx, a.service, owner, agentlink.TaskSubmit, agentlink.Call{Body: operationBody(t, newRequest)}); err == nil {
		t.Fatal("revoked link acquired new execution")
	}
}

func TestAgentLinkTaskConfigurationUsesSerializableOwnerCapability(t *testing.T) {
	b, _ := linkBackendFixture(t)
	ctx := context.Background()
	request := protocol.CreateTask{RequestID: "configured-task", Text: "Configured task\nUse the existing owner model."}
	input := agentlink.TaskRequest{From: protocol.AgentRef{NodeID: "node", Kind: "assistant", AgentID: "main"}, Target: agentlink.AssistantTarget(b.s.options.RuntimeID, "main"), RequestID: request.RequestID, Text: request.Text, Configuration: &request}
	raw := operationBody(t, input)
	var decoded agentlink.TaskRequest
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Configuration == nil {
		t.Fatal("configuration disappeared across serialization", string(raw), err)
	}
	accepted, err := b.s.agentLinks.service.Submit(ctx, "", decoded)
	if err != nil {
		t.Fatal(err)
	}
	state, err := b.s.agentLinks.service.Status(ctx, "", input.From, accepted.ID)
	if err != nil || state.ID != accepted.ID {
		t.Fatal(state, err)
	}
	for _, kind := range []string{"user", "task"} {
		forged := decoded
		forged.From.Kind = kind
		if _, err = b.s.agentLinks.service.Submit(ctx, "", forged); err == nil {
			t.Fatal("non-owner acquired configuration capability", kind)
		}
	}
	directory, err := b.Directory(ctx)
	if err != nil || directory.Capabilities(agentlink.AssistantTarget(directory.NodeID, "main")).Supports(agentlink.TaskConfigure) {
		t.Fatal("owner capability leaked to peers", directory, err)
	}
}

func TestAgentLinkUserTaskReceiptRetainsFullCallerScope(t *testing.T) {
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
