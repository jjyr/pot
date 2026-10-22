package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestAgentToolsShareTaskPortAndCatalogueAcrossLocalAndPeer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { batchReply(w, "task result") }))
	defer model.Close()
	source := taskControl(t, "source", model.URL)
	receiver := taskControl(t, "receiver", model.URL)
	a := &agentControlFixture{service: source, endpoint: controlPeerEndpoint(source)}
	b := &agentControlFixture{service: receiver, endpoint: controlPeerEndpoint(receiver)}
	link := connectAgentControls(t, a, b)
	requests := []protocol.CreateTask{{Text: "Local task\nReturn a result."}, {Text: "Peer task\nReturn a result.", LinkID: link.ID}}
	ids := []string{}
	for i, request := range requests {
		body, _ := json.Marshal(request)
		accepted, err := source.productCall(ctx, "main", []string{"local-request", "peer-request"}[i], "create_task", body)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, accepted.(protocol.TaskInfo).ID)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		source.syncTaskExecutions(ctx)
		complete := 0
		for _, id := range ids {
			record, err := source.Tasks.Get(ctx, "main", id)
			if err != nil || record == nil {
				t.Fatal(record, err)
			}
			if record.Info.Phase == protocol.TaskComplete {
				complete++
			}
		}
		if complete == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tasks did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	page, err := source.productCall(ctx, "main", "list", "list_tasks", json.RawMessage(`{"scope":"all"}`))
	if err != nil || page.(protocol.TaskPage).Total != 2 {
		t.Fatal(page, err)
	}
	for _, id := range ids {
		body, _ := json.Marshal(map[string]string{"task_id": id})
		summary, err := source.productCall(ctx, "main", "read", "get_task", body)
		if err != nil {
			t.Fatal(err)
		}
		result := summary.(map[string]any)["result"].(map[string]any)
		if result["text"] != "task result" {
			t.Fatal(summary)
		}
	}
	local, _ := source.Tasks.Get(ctx, "main", ids[0])
	peer, _ := source.Tasks.Get(ctx, "main", ids[1])
	if local.Config == nil || local.Execution != nil || peer.Config != nil || peer.Launch != nil || peer.Execution.Receipt.ID == "" {
		t.Fatal("adapter state leaked into generic task", local, peer)
	}
}
