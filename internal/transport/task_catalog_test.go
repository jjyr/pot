package transport

import (
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/protocol"
	"net/http/httptest"
	"testing"
)

func TestTaskCatalogHTTPFiltersAndValidation(t *testing.T) {
	app := historyApplication(t, 0)
	owner := localAssistantID(t, app.Control)
	handler := Handler(app, "private")
	// Terminal synthetic records in the disposable backend; no execution or model.
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("fixture-%d", i)
		itemOwner, phase := owner, protocol.TaskComplete
		if i == 4 {
			itemOwner = "other-owner"
		}
		if i == 5 {
			phase = protocol.TaskFailed
		}
		record := tasks.Record{AssistantID: itemOwner, Request: protocol.CreateTask{RequestID: id, Text: "Synthetic HTTP catalogue"}, Info: protocol.TaskInfo{ID: id, AssistantID: itemOwner, Title: id, Phase: phase, AgentLink: &protocol.AgentLinkOrigin{NodeID: "fixture"}}, Execution: &tasks.Execution{}}
		if err := app.Control.Tasks.AcceptExecution(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		query           string
		status          int
		total, complete uint64
		rows            int
		next            bool
	}{
		{"scope=complete&phase=complete&assistant_id=" + owner + "&limit=2", 200, 4, 4, 2, true},
		{"scope=complete&assistant_id=" + owner + "&offset=2&limit=2", 200, 4, 4, 2, false},
		{"scope=complete&assistant_id=agent-link:link-a:same", 200, 0, 0, 0, false},
		{"scope=unarchived&phase=failed&assistant_id=" + owner, 200, 1, 4, 1, false},
		{"scope=complete", 200, 5, 5, 5, false},
		{"scope=complete&phase=completed", 400, 0, 0, 0, false},
		{"scope=unknown", 400, 0, 0, 0, false},
	} {
		request := httptest.NewRequest("GET", "/api/v1/tasks/catalog?"+test.query, nil)
		request.Header.Set("Authorization", "Bearer private")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatal(test.query, response.Code, response.Body.String())
		}
		if response.Code == 200 {
			var page protocol.TaskPage
			if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || page.Total != test.total || page.Counts.Complete != test.complete || len(page.Items) != test.rows || (page.NextOffset != nil) != test.next {
				t.Fatal(test.query, page, err)
			}
		}
	}
	request := httptest.NewRequest("GET", "/api/v1/tasks/catalog?scope=complete", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("unauthenticated catalogue", response.Code)
	}
}
