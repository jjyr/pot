package transport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestExecutionStreamPushesTerminalWithoutEndingSubscription(t *testing.T) {
	app := historyApplication(t, 0)
	owner := localAssistantID(t, app.Control)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	c, err := app.Control.Conversation(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "streamed", ConversationID: owner, Text: "reply"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(Handler(app, "private"))
	defer server.Close()
	header := map[string][]string{"Authorization": {"Bearer private"}}
	conn, _, err := websocket.Dial(ctx, server.URL+"/api/v1/ws", &websocket.DialOptions{HTTPHeader: header, Subprotocols: []string{"pot.v1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	write := func(value any) {
		raw, _ := json.Marshal(value)
		if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
	}
	write(map[string]any{"type": "subscribe", "id": "execution", "path": "/api/v1/execution-status?assistant_id=" + url.QueryEscape(owner) + "&request_id=streamed"})
	var terminal protocol.AgentExecutionStatus
	events := 0
	for !terminal.Terminal {
		_, raw, e := conn.Read(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var f struct {
			Type, Event string
			Data        json.RawMessage
		}
		if e = json.Unmarshal(raw, &f); e != nil {
			t.Fatal(e)
		}
		if f.Type == "end" {
			t.Fatalf("subscription ended: %s", raw)
		}
		if f.Type != "event" {
			continue
		}
		if f.Event != "execution_status" {
			t.Fatal(f.Event)
		}
		events++
		if strings.Contains(string(f.Data), "part-") {
			t.Fatal("reply body leaked into lightweight status")
		}
		if e = json.Unmarshal(f.Data, &terminal); e != nil {
			t.Fatal(e)
		}
	}
	if terminal.State != "completed" || len(terminal.ReplyIDs) != 1 || terminal.ConversationCursor == 0 || events > 8 {
		t.Fatalf("unexpected state or token-level notifications: %+v events=%d", terminal, events)
	}
	// Another request/response round trip proves the terminal event did not end
	// the subscription. Any premature end queued before this response is a bug.
	write(map[string]any{"type": "request", "id": "probe", "method": "GET", "path": "/api/v1/runtime"})
	for {
		_, raw, e := conn.Read(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var f struct{ Type, ID string }
		json.Unmarshal(raw, &f)
		if f.Type == "end" && f.ID == "execution" {
			t.Fatal("terminal snapshot ended subscription")
		}
		if f.Type == "response" && f.ID == "probe" {
			break
		}
	}
	write(map[string]any{"type": "unsubscribe", "id": "execution"})
}
