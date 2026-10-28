package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestOutboxDispatchRejectsReplacementControlBeforeDirectoryRecovery(t *testing.T) {
	app := historyApplication(t, 0)
	owner := localAssistantID(t, app.Control)
	handler := Handler(app, "synthetic-token")
	body := json.RawMessage(fmt.Sprintf(`{"request_id":"fixed-outbox-id","conversation_id":%q,"text":"Synthetic queued input"}`, owner))
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/v1/messages"}, {"POST", "/api/v1/turn/steer"}, {"POST", "/api/v1/turn/retry"},
		{"GET", "/api/v1/messages/receipt"}, {"GET", "/api/v1/snapshot"},
		{"POST", "/api/v1/attachments"}, {"GET", "/api/v1/attachments/synthetic-file"},
	} {
		for _, control := range []string{"old-control", ""} {
			path := route.path + "?assistant_id=" + owner + "&expected_control_id=" + control
			_, err := app.Call(t.Context(), Request{Method: route.method, Path: path, Body: body})
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != "control_changed" {
				t.Fatal("old native request reached replacement dispatch", route, err)
			}
			request := httptest.NewRequest(route.method, path, strings.NewReader(string(body)))
			request.Header.Set("Authorization", "Bearer synthetic-token")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusConflict {
				t.Fatal("old HTTP request reached replacement dispatch", route, response.Code, response.Body.String())
			}
		}
	}
	query := "?assistant_id=" + owner + "&expected_control_id=history-fixture"
	receiptPath := "/api/v1/messages/receipt" + query + "&request_id=fixed-outbox-id&conversation_id=" + owner
	read := func() protocol.SendReceipt {
		t.Helper()
		value, err := app.Call(t.Context(), Request{Method: "GET", Path: receiptPath})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(value)
		var receipt protocol.SendReceipt
		if err := json.Unmarshal(data, &receipt); err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	if receipt := read(); receipt.State != "unconfirmed" {
		t.Fatal("rejected requests changed business acceptance", receipt)
	}
	if _, err := app.Call(t.Context(), Request{Method: "POST", Path: "/api/v1/messages" + query, Body: body}); err != nil {
		t.Fatal("current Control rejected native submission", err)
	}
	first := read()
	if first.State != "accepted" || first.RequestID != "fixed-outbox-id" || first.RuntimeID != app.RuntimeID || first.OwnerID != owner || first.ConversationID != owner {
		t.Fatal("accepted receipt changed scope", first)
	}
	app.BootID = "later-boot"
	if receipt := read(); receipt != first {
		t.Fatal("same Control boot change lost durable receipt", receipt, first)
	}
	request := httptest.NewRequest("POST", "/api/v1/messages"+query, strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer synthetic-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var accepted protocol.SendMessageAccepted
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil || response.Code != http.StatusOK || !accepted.Duplicate || accepted.MessageID != first.MessageID {
		t.Fatal("same Control new boot could not deduplicate HTTP submission", response.Code, response.Body.String(), err)
	}
}
