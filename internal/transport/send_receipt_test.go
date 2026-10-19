package transport

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/jjyr/pot/internal/assistant"
)

func TestSendReceiptHTTPIdentityAndAuthorization(t *testing.T) {
	app := historyApplication(t, 0)
	handler := Handler(app, "private")
	for _, test := range []struct {
		query, token string
		code         int
	}{
		{"request_id=not-sent&conversation_id=main", "wrong", 401},
		{"request_id=not-sent&conversation_id=other", "private", 400},
		{"request_id=not-sent&conversation_id=main&assistant_id=missing", "private", 400},
		{"request_id=not-sent&conversation_id=main", "private", 200},
	} {
		r := httptest.NewRequest("GET", "/api/v1/messages/receipt?"+test.query, nil)
		r.Header.Set("Authorization", "Bearer "+test.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != test.code {
			t.Fatalf("%s: %d %s", test.query, w.Code, w.Body.String())
		}
		if w.Code == 200 {
			var value assistant.SendReceipt
			if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			if value.State != "unconfirmed" || value.RequestID != "not-sent" || value.ConversationID != "main" || value.OwnerID != "main" || value.RuntimeID != "history-fixture" {
				t.Fatal(value)
			}
		}
	}
}
