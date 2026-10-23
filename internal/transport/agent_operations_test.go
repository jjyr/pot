package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestUnifiedHTTPPortRunsLinkedTaskWithoutLocalAssistant(t *testing.T) {
	ctx := context.Background()
	receiver := historyApplication(t, 0)
	view, err := receiver.Control.AgentLinks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "tcp://" + view.(map[string]any)["network"].(map[string]any)["listen"].(string)
	if _, err = receiver.Control.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "configure", Mode: "address", Endpoint: endpoint}); err != nil {
		t.Fatal(err)
	}
	invitation, err := receiver.Control.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "invite"})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := execution.New("http-user-source", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	source, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "http-user-source", BootID: "boot", Keys: keys, Manager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { source.Close() })
	value, err := source.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "connect", Address: invitation.(map[string]any)["address"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	link := value.(agentlink.Connection)
	owner := "agent-link:" + link.ID + ":" + localAssistantID(t, receiver.Control)
	query := "?assistant_id=" + url.QueryEscape(owner)
	server := httptest.NewServer(Handler(&Application{Control: source, RuntimeID: "http-user-source", BootID: "boot"}, "private"))
	defer server.Close()
	call := func(ctx context.Context, method, path string, body any) (int, []byte) {
		t.Helper()
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		request, _ := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(data))
		request.Header.Set("Authorization", "Bearer private")
		request.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err = io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, data
	}
	status, data := call(ctx, "GET", "/api/v1/agent-links/capabilities"+query, nil)
	var description agentlink.Description
	if err = json.Unmarshal(data, &description); err != nil || status != 200 || !description.Supports(agentlink.TaskSubmit) || description.Supports(agentlink.AssistantConfigure) {
		t.Fatal(status, string(data), err)
	}
	request := protocol.CreateTask{RequestID: "http-user-task", Text: "Read report\nReturn a result through the original conversation."}
	ready, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		status, data = call(ready, "POST", "/api/v1/tasks"+query, request)
		var api protocol.APIError
		if status != http.StatusConflict || json.Unmarshal(data, &api) != nil || api.Code != agentlink.ErrCapacity.Error() {
			break
		}
		// Observation can occupy the peer's one-operation limit. Only this
		// explicit pre-execution refusal is safe to retry with the same request.
		select {
		case <-ready.Done():
			t.Fatal("peer admission did not become ready", ready.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	var task protocol.TaskInfo
	if err = json.Unmarshal(data, &task); err != nil || status != 200 || task.AssistantID != owner {
		t.Fatal(status, string(data), err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		status, data = call(ctx, "GET", "/api/v1/tasks/"+task.ID+query, nil)
		var record struct {
			Info         protocol.TaskInfo
			Capabilities agentlink.Description
		}
		if err = json.Unmarshal(data, &record); err != nil || status != 200 {
			t.Fatal(status, string(data), err)
		}
		if record.Info.Phase == protocol.TaskComplete {
			if record.Capabilities.Supports(agentlink.TaskInput) || !record.Capabilities.Supports(agentlink.TaskCancel) {
				t.Fatal("task controls ignore effective capability", record.Capabilities)
			}
			break
		}
		if record.Info.Phase == protocol.TaskFailed || time.Now().After(deadline) {
			t.Fatal("linked HTTP task did not complete", string(data))
		}
		time.Sleep(20 * time.Millisecond)
	}
	for {
		status, data = call(ctx, "GET", "/api/v1/snapshot"+query, nil)
		var frame protocol.UIFrame
		if err = json.Unmarshal(data, &frame); err != nil || status != 200 {
			t.Fatal(status, string(data), err)
		}
		if len(frame.Messages) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("result did not enter user chat", string(data))
		}
		time.Sleep(20 * time.Millisecond)
	}
	status, data = call(ctx, "POST", "/api/v1/turn/cancel"+query, map[string]string{"request_id": "stop", "user_message_id": "user"})
	if status < 400 {
		t.Fatal("unsupported remote control fell back to local agent", string(data))
	}
	defs, err := source.Registry.Definitions(ctx)
	if err != nil || len(defs) != 0 {
		t.Fatal("HTTP task created a local assistant", defs, err)
	}
}
