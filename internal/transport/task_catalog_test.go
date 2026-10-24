package transport

import (
	"encoding/json"
	"github.com/jjyr/pot/pkg/protocol"
	"net/http/httptest"
	"testing"
)

func TestTaskCatalogHTTPFiltersAndValidation(t *testing.T) {
	app := historyApplication(t, 0)
	owner := localAssistantID(t, app.Control)
	handler := Handler(app, "private")
	// Empty isolated catalogue still exercises real HTTP parsing and backend validation.
	for _, test := range []struct {
		query  string
		status int
	}{
		{"scope=complete&phase=complete&assistant_id=" + owner, 200},
		{"scope=complete&assistant_id=agent-link:link-a:same", 200},
		{"scope=unarchived&phase=failed&assistant_id=" + owner + "&offset=20&limit=20", 200},
		{"scope=complete&phase=completed", 400},
		{"scope=unknown", 400},
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
			if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || page.Total != 0 || page.Counts.Complete != 0 || page.NextOffset != nil {
				t.Fatal(page, err)
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
